package agy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

const transcript = `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-06-09T21:02:00Z","content":"<USER_REQUEST>\nis every plugin updatable\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-06-09T14:02:00-07:00.\n</ADDITIONAL_METADATA>"}
{"step_index":1,"source":"SYSTEM","type":"EPHEMERAL_MESSAGE","status":"DONE","created_at":"2026-06-09T21:02:01Z","content":"reminders"}
{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-06-09T21:02:05Z","content":"Let me check the installed plugins."}
{"step_index":3,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-06-09T21:02:16Z","content":"Output: xExtension-ArticleSummary"}
{"step_index":4,"source":"MODEL","type":"VIEW_FILE","status":"DONE","created_at":"2026-06-09T21:02:20Z","content":"File Path: sync.sh"}
{"step_index":5,"source":"SYSTEM","type":"CHECKPOINT","status":"DONE","created_at":"2026-06-09T21:05:00Z","content":"{{ CHECKPOINT 0 }} summary of the truncated context"}
{"step_index":6,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-06-09T21:06:00Z","content":"<USER_REQUEST>\nupdate them all\n</USER_REQUEST>"}
{"step_index":7,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-06-09T21:06:10Z","content":"All plugins updated."}
`

// writeConversation lays out one brain transcript and returns the root.
func writeConversation(t *testing.T, root, id, body string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(root, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func writeHistory(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "history.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testRoot(t *testing.T) session.Roots {
	t.Helper()
	root := t.TempDir()
	newer := time.Date(2026, 6, 9, 21, 6, 0, 0, time.UTC)
	older := newer.Add(-time.Hour)

	writeConversation(t, root, "conv-main", transcript, newer)
	writeConversation(t, root, "conv-other",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-06-09T20:00:00Z","content":"<USER_REQUEST>\nfix the deploy\n</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-06-09T20:00:05Z","content":"Deploy fixed."}
`, older)
	// A subagent conversation: has a transcript but no history.jsonl entry.
	writeConversation(t, root, "conv-sub",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-06-09T20:30:00Z","content":"<USER_REQUEST>\nrun the checks\n</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-06-09T20:30:05Z","content":"Checks pass."}
`, older.Add(30*time.Minute))

	// conv-other is a single-prompt conversation: its only history line was
	// written before the conversation id existed, so it has no conversationId
	// and is joined back by matchStart. The decoy line shares its display text
	// but sits in another workspace with a far timestamp.
	deployTS := time.Date(2026, 6, 9, 20, 0, 3, 0, time.UTC).UnixMilli()
	decoyTS := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC).UnixMilli()
	writeHistory(t, root, fmt.Sprintf(
		`{"display":"is every plugin updatable","timestamp":1,"workspace":"/home/u/freshrss","conversationId":"conv-main"}
{"display":"update them all","timestamp":2,"workspace":"/home/u/freshrss","conversationId":"conv-main"}
{"display":"fix the deploy","timestamp":%d,"workspace":"/home/u/elsewhere"}
{"display":"fix the deploy","timestamp":%d,"workspace":"/home/u/deploy"}
{"display":"an id-less line matching no transcript","timestamp":4,"workspace":"/home/u/old"}
`, decoyTS, deployTS))
	return session.Roots{Agy: root}
}

func TestReadAgySession(t *testing.T) {
	roots := testRoot(t)
	p := New()
	ctx := context.Background()

	src, err := p.Resolve(ctx, roots, "conv-main")
	if err != nil {
		t.Fatal(err)
	}
	if src.Metadata["cwd"] != "/home/u/freshrss" {
		t.Errorf("cwd = %q, want /home/u/freshrss", src.Metadata["cwd"])
	}
	if src.Metadata["title"] != "is every plugin updatable" {
		t.Errorf("title = %q, want the opening prompt", src.Metadata["title"])
	}

	thread, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	want := []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "is every plugin updatable"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "Let me check the installed plugins."},
		{Kind: session.KindCompact, Text: "{{ CHECKPOINT 0 }} summary of the truncated context"},
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "update them all"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "All plugins updated."},
	}
	if len(thread.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(thread.Entries), len(want), thread.Entries)
	}
	for i, w := range want {
		g := thread.Entries[i]
		if g.Kind != w.Kind || g.Role != w.Role || g.Text != w.Text {
			t.Errorf("entry %d = {%s %s %q}, want {%s %s %q}", i, g.Kind, g.Role, g.Text, w.Kind, w.Role, w.Text)
		}
	}
	if thread.Entries[0].Time.IsZero() {
		t.Error("entry 0 has no timestamp")
	}
}

func TestListAgySessions(t *testing.T) {
	roots := testRoot(t)
	p := New()
	ctx := context.Background()

	// Unfiltered: every conversation with a transcript, newest first.
	all, err := p.List(ctx, roots, session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d sessions, want 3", len(all))
	}
	if all[0].Ref.SessionID != "conv-main" || all[0].Rank != 1 {
		t.Errorf("newest = %s rank %d, want conv-main rank 1", all[0].Ref.SessionID, all[0].Rank)
	}

	// Cwd filter matches history.jsonl workspace and drops the subagent.
	sums, err := p.List(ctx, roots, session.ListOptions{Cwd: "/home/u/freshrss"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "conv-main" {
		t.Fatalf("cwd filter got %+v, want only conv-main", sums)
	}
	if sums[0].Preview != "is every plugin updatable" {
		t.Errorf("preview = %q", sums[0].Preview)
	}

	// Query matches visible text only.
	byQuery, err := p.List(ctx, roots, session.ListOptions{Query: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byQuery) != 1 || byQuery[0].Ref.SessionID != "conv-other" {
		t.Fatalf("query got %+v, want only conv-other", byQuery)
	}

	// conv-other's history line has no conversationId (single-prompt
	// conversation); matchStart joins it by first-turn text, and the closest
	// timestamp beats the decoy workspace sharing the same display.
	joined, err := p.List(ctx, roots, session.ListOptions{Cwd: "/home/u/deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(joined) != 1 || joined[0].Ref.SessionID != "conv-other" {
		t.Fatalf("cwd join got %+v, want only conv-other", joined)
	}
	if joined[0].Title != "fix the deploy" {
		t.Errorf("joined title = %q, want the opening prompt", joined[0].Title)
	}
	if wrong, _ := p.List(ctx, roots, session.ListOptions{Cwd: "/home/u/elsewhere"}); len(wrong) != 0 {
		t.Errorf("decoy workspace matched %+v, want none", wrong)
	}
}

// Resolving by id constructs the transcript path directly, so a conversation
// absent from history.jsonl (a subagent's, or one predating conversationId) is
// still reachable — just without cwd/title metadata.
func TestResolveSubagentByID(t *testing.T) {
	roots := testRoot(t)
	p := New()
	ctx := context.Background()

	src, err := p.Resolve(ctx, roots, "conv-sub")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "conv-sub" {
		t.Errorf("id = %s, want conv-sub", src.Ref.SessionID)
	}
	if src.Metadata["cwd"] != "" {
		t.Errorf("cwd = %q, want empty (conv-sub is not in history.jsonl)", src.Metadata["cwd"])
	}
	thread, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Entries) != 2 {
		t.Errorf("got %d entries, want 2", len(thread.Entries))
	}
}

func TestResolveNewestAndMissing(t *testing.T) {
	roots := testRoot(t)
	p := New()
	ctx := context.Background()

	src, err := p.Resolve(ctx, roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "conv-main" {
		t.Errorf("newest = %s, want conv-main", src.Ref.SessionID)
	}
	if src.Ref.Provider != session.ProviderAgy {
		t.Errorf("provider = %q", src.Ref.Provider)
	}
	if _, err := p.Resolve(ctx, roots, "nope"); err == nil {
		t.Error("want error for unknown id")
	}
}
