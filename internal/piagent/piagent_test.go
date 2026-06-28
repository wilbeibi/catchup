package piagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

const transcript = `{"type":"session","version":3,"id":"019f-pi","timestamp":"2026-06-28T02:50:19.365Z","cwd":"/home/u/src/catchup"}
{"type":"model_change","id":"m0","parentId":null,"timestamp":"2026-06-28T02:50:20.204Z","provider":"openrouter","modelId":"anthropic/claude-sonnet-4"}
{"type":"message","id":"m1","parentId":"m0","timestamp":"2026-06-28T02:50:23.414Z","message":{"role":"user","timestamp":1793155823414,"content":[{"type":"text","text":"support pi agent"}]}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-06-28T02:50:26.242Z","message":{"role":"assistant","timestamp":1793155826242,"provider":"anthropic","model":"claude-sonnet-4","content":[{"type":"thinking","thinking":"hidden"},{"type":"toolCall","id":"tc1","name":"read","arguments":{"file":"x"}},{"type":"text","text":"I will inspect the format."}]}}
{"type":"message","id":"m3","parentId":"m2","timestamp":"2026-06-28T02:50:26.266Z","message":{"role":"toolResult","toolCallId":"tc1","toolName":"read","content":[{"type":"text","text":"tool output"}]}}
{"type":"compaction","id":"c1","parentId":"m3","timestamp":"2026-06-28T02:51:00.000Z","summary":"summary so far","firstKeptEntryId":"m1"}
{"type":"session_info","id":"n1","parentId":"c1","timestamp":"2026-06-28T02:51:01.000Z","name":"Pi support"}
{"type":"message","id":"m4","parentId":"n1","timestamp":"2026-06-28T02:51:02.000Z","message":{"role":"user","content":[{"type":"text","text":"finish it"}]}}
{"type":"message","id":"m5","parentId":"m4","timestamp":"2026-06-28T02:51:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}
`

func writeTranscript(t *testing.T, root, dir, name, body string, mod time.Time) {
	t.Helper()
	path := filepath.Join(root, "sessions", dir)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, name)
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestReadPiAgentSession(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "--home-u-src-catchup--", "2026-06-28T02-50-19-365Z_019f-pi.jsonl", transcript, time.Now())

	p := New()
	src, err := p.Resolve(context.Background(), session.Roots{PiAgent: root}, "019f-pi")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.Provider != session.ProviderPiAgent || src.Ref.SessionID != "019f-pi" {
		t.Fatalf("source ref = %+v", src.Ref)
	}
	if src.Metadata["title"] != "Pi support" || src.Metadata["cwd"] != "/home/u/src/catchup" {
		t.Fatalf("metadata = %+v", src.Metadata)
	}
	if src.Metadata["model"] != "claude-sonnet-4" || src.Metadata["model_provider"] != "anthropic" {
		t.Fatalf("model metadata = %+v", src.Metadata)
	}

	thread, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	got := []struct {
		kind string
		role string
		text string
	}{}
	for _, e := range thread.Entries {
		got = append(got, struct {
			kind string
			role string
			text string
		}{e.Kind, e.Role, e.Text})
	}
	want := []struct {
		kind string
		role string
		text string
	}{
		{session.KindMessage, session.RoleUser, "support pi agent"},
		{session.KindMessage, session.RoleAssistant, "I will inspect the format."},
		{session.KindCompact, "", "summary so far"},
		{session.KindMessage, session.RoleUser, "finish it"},
		{session.KindMessage, session.RoleAssistant, "done"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, e := range thread.Entries {
		if strings.Contains(e.Text, "tool output") || strings.Contains(e.Text, "hidden") {
			t.Fatalf("tool/thinking noise leaked into timeline: %+v", thread.Entries)
		}
	}
}

func TestListPiAgentSessions(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeTranscript(t, root, "--home-u-src-catchup--", "2026-06-28T02-50-19-365Z_019f-pi.jsonl", transcript, now)
	writeTranscript(t, root, "--home-u-other--", "2026-06-27T01-00-00-000Z_older.jsonl",
		`{"type":"session","version":3,"id":"older","timestamp":"2026-06-27T01:00:00Z","cwd":"/home/u/other"}
{"type":"message","id":"m1","timestamp":"2026-06-27T01:00:01Z","message":{"role":"user","content":[{"type":"text","text":"unrelated"}]}}
`, now.Add(-time.Hour))

	p := New()
	sums, err := p.List(context.Background(), session.Roots{PiAgent: root}, session.ListOptions{
		Cwd:   "/home/u/src/catchup",
		Query: "finish",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "019f-pi" {
		t.Fatalf("summaries = %+v", sums)
	}
	if sums[0].Rank != 1 || sums[0].Preview != "support pi agent" {
		t.Fatalf("summary fields = %+v", sums[0])
	}
}

func TestReadFollowsCurrentBranch(t *testing.T) {
	root := t.TempDir()
	body := `{"type":"session","version":3,"id":"branchy","timestamp":"2026-06-28T02:50:19.365Z","cwd":"/home/u/src/catchup"}
{"type":"message","id":"root","parentId":null,"timestamp":"2026-06-28T02:50:20Z","message":{"role":"user","content":"root question"}}
{"type":"message","id":"old","parentId":"root","timestamp":"2026-06-28T02:50:21Z","message":{"role":"assistant","content":[{"type":"text","text":"abandoned answer"}]}}
{"type":"message","id":"new","parentId":"root","timestamp":"2026-06-28T02:50:22Z","message":{"role":"assistant","content":[{"type":"text","text":"current answer"}]}}
`
	writeTranscript(t, root, "--home-u-src-catchup--", "2026-06-28T02-50-19-365Z_branchy.jsonl", body, time.Now())

	src, err := New().Resolve(context.Background(), session.Roots{PiAgent: root}, "branchy")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := New().Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Entries) != 2 {
		t.Fatalf("got %d entries, want current branch only: %+v", len(thread.Entries), thread.Entries)
	}
	if thread.Entries[0].Text != "root question" || thread.Entries[1].Text != "current answer" {
		t.Fatalf("wrong branch rendered: %+v", thread.Entries)
	}
	for _, e := range thread.Entries {
		if strings.Contains(e.Text, "abandoned") {
			t.Fatalf("abandoned branch leaked: %+v", thread.Entries)
		}
	}
}
