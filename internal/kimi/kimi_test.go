package kimi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

// wire14 is a protocol-1.4 log: user prompts as append_message with origin,
// assistant text as content.part loop events, an injection reminder, a
// compaction, and think/tool noise that must not reach the timeline.
const wire14 = `{"type":"metadata","protocol_version":"1.4","created_at":1784292334522}
{"type":"config.update","modelAlias":"moonshot-ai/kimi-k2.7-code","time":1784292334607}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"support kimi"}],"origin":{"kind":"user"}},"time":1784292334633}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"<system-reminder>\nAuto permission mode is active.\n</system-reminder>"}],"origin":{"kind":"injection","variant":"permission_mode"}},"time":1784292334634}
{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"think","think":"hidden reasoning"}},"time":1784292341000}
{"type":"context.append_loop_event","event":{"type":"tool.call","toolCallId":"Write_0","name":"Write","args":{"path":"x"}},"time":1784292341100}
{"type":"context.append_loop_event","event":{"type":"tool.result","toolCallId":"Write_0","result":{"output":"tool output"}},"time":1784292341200}
{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"0","part":{"type":"text","text":"I will inspect the format."}},"time":1784292346295}
{"type":"context.apply_compaction","summary":"summary so far","compactedCount":4,"tokensBefore":9000,"tokensAfter":900,"time":1784292350000}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"finish it"}],"origin":{"kind":"user"}},"time":1784292351000}
{"type":"context.append_loop_event","event":{"type":"content.part","turnId":"1","part":{"type":"text","text":"done"}},"time":1784292352000}
`

// wire10 is a protocol-1.0 log migrated from the legacy kimi-cli: no origin
// fields, no time fields, assistant text inline in append_message parts.
const wire10 = `{"type":"metadata","protocol_version":"1.0","created_at":1770088966601}
{"type":"context.append_message","message":{"role":"user","content":[{"type":"text","text":"pwd"}],"toolCalls":[]}}
{"type":"context.append_message","message":{"role":"assistant","content":[{"type":"think","think":"hidden"},{"type":"text","text":"You are in /home/u."}],"toolCalls":[]}}
{"type":"context.append_message","message":{"role":"tool","content":[{"type":"text","text":"tool output"}],"toolCalls":[]}}
`

func writeSession(t *testing.T, root, wd, id, state, wire string) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", wd, id)
	if err := os.MkdirAll(filepath.Join(dir, "agents", "main"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agents", "main", "wire.jsonl"), []byte(wire), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestReadKimiSession(t *testing.T) {
	root := t.TempDir()
	dir := writeSession(t, root, "wd_catchup_abc", "session_new",
		`{"createdAt":"2026-07-17T12:45:34.477Z","updatedAt":"2026-07-17T12:45:34.628Z","title":"Kimi support","workDir":"/home/u/src/catchup"}`,
		wire14)
	wireMod := time.Date(2026, 7, 17, 12, 45, 52, 628e6, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, "agents", "main", "wire.jsonl"), wireMod, wireMod); err != nil {
		t.Fatal(err)
	}

	p := New()
	src, err := p.Resolve(context.Background(), session.Roots{Kimi: root}, "session_new")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.Provider != session.ProviderKimi || src.Ref.SessionID != "session_new" {
		t.Fatalf("source ref = %+v", src.Ref)
	}
	if src.Metadata["title"] != "Kimi support" || src.Metadata["cwd"] != "/home/u/src/catchup" {
		t.Fatalf("metadata = %+v", src.Metadata)
	}
	want := time.Date(2026, 7, 17, 12, 45, 52, 628e6, time.UTC)
	if !src.UpdatedAt.Equal(want) {
		t.Fatalf("updatedAt = %v, want %v", src.UpdatedAt, want)
	}

	thread, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if thread.Source.Metadata["model"] != "moonshot-ai/kimi-k2.7-code" {
		t.Fatalf("model metadata = %+v", thread.Source.Metadata)
	}
	wantEntries := []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "support kimi"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "I will inspect the format."},
		{Kind: session.KindCompact, Text: "summary so far"},
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "finish it"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "done"},
	}
	if len(thread.Entries) != len(wantEntries) {
		t.Fatalf("got %d entries, want %d: %+v", len(thread.Entries), len(wantEntries), thread.Entries)
	}
	for i, w := range wantEntries {
		g := thread.Entries[i]
		if g.Kind != w.Kind || g.Role != w.Role || g.Text != w.Text {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}
	}
	for _, e := range thread.Entries {
		if strings.Contains(e.Text, "tool output") || strings.Contains(e.Text, "hidden") ||
			strings.Contains(e.Text, "system-reminder") {
			t.Fatalf("noise leaked into timeline: %+v", thread.Entries)
		}
	}
}

func TestReadLegacyProtocol10(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "wd_home_abc", "ses_old",
		`{"createdAt":"2026-02-03T04:02:46.601Z","updatedAt":"2026-02-03T04:03:00.000Z","title":"","workDir":"/home/u"}`,
		wire10)

	p := New()
	src, err := p.Resolve(context.Background(), session.Roots{Kimi: root}, "ses_old")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Entries) != 2 {
		t.Fatalf("got %d entries: %+v", len(thread.Entries), thread.Entries)
	}
	if thread.Entries[0].Role != session.RoleUser || thread.Entries[0].Text != "pwd" {
		t.Fatalf("entry 0 = %+v", thread.Entries[0])
	}
	if thread.Entries[1].Role != session.RoleAssistant || thread.Entries[1].Text != "You are in /home/u." {
		t.Fatalf("entry 1 = %+v", thread.Entries[1])
	}
}

func TestListKimiSessions(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "wd_catchup_abc", "session_new",
		`{"createdAt":"2026-07-17T12:45:34.477Z","updatedAt":"2026-07-17T12:45:52.628Z","title":"Kimi support","workDir":"/home/u/src/catchup"}`,
		wire14)
	writeSession(t, root, "wd_home_abc", "ses_old",
		`{"createdAt":"2026-02-03T04:02:46.601Z","updatedAt":"2026-02-03T04:03:00.000Z","title":"","workDir":"/home/u"}`,
		wire10)

	p := New()
	sums, err := p.List(context.Background(), session.Roots{Kimi: root}, session.ListOptions{
		Cwd:   "/home/u/src/catchup",
		Query: "finish",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "session_new" {
		t.Fatalf("summaries = %+v", sums)
	}
	if sums[0].Rank != 1 || sums[0].Preview != "support kimi" || sums[0].Title != "Kimi support" {
		t.Fatalf("summary fields = %+v", sums[0])
	}
}

// TestWorkDirFallsBackToIndex covers sessions written before kimi-code 0.26,
// whose state.json has no workDir: the append-only session_index.jsonl is the
// only record of their working directory, and its last row for an id wins.
func TestWorkDirFallsBackToIndex(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "wd_home_abc", "ses_old",
		`{"createdAt":"2026-02-03T04:02:46.601Z","updatedAt":"2026-02-03T04:03:00.000Z","title":"old","workDir":""}`,
		wire10)
	index := `{"sessionId":"ses_old","sessionDir":"x","workDir":"/stale"}
{"sessionId":"ses_old","sessionDir":"x","workDir":"/home/u"}
`
	if err := os.WriteFile(filepath.Join(root, "session_index.jsonl"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}

	sums, err := New().List(context.Background(), session.Roots{Kimi: root}, session.ListOptions{Cwd: "/home/u"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "ses_old" || sums[0].Cwd != "/home/u" {
		t.Fatalf("summaries = %+v", sums)
	}
}

func TestResolveNewestByWireMtime(t *testing.T) {
	root := t.TempDir()
	// The session that is "newer by state.json" has the staler wire log; the
	// wire must win, because updatedAt is only rewritten around turn
	// boundaries while the log advances with every record.
	writeSession(t, root, "wd_a", "ses_active",
		`{"updatedAt":"2026-07-01T00:00:00.000Z","title":"active","workDir":"/w"}`, wire14)
	dir := writeSession(t, root, "wd_b", "ses_stale",
		`{"updatedAt":"2026-07-16T00:00:00.000Z","title":"stale","workDir":"/w"}`, wire14)
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "agents", "main", "wire.jsonl"), old, old); err != nil {
		t.Fatal(err)
	}

	src, err := New().Resolve(context.Background(), session.Roots{Kimi: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "ses_active" {
		t.Fatalf("newest = %+v", src.Ref)
	}
}
