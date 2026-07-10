package agy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

	writeHistory(t, root,
		`{"display":"is every plugin updatable","timestamp":1,"workspace":"/home/u/freshrss","conversationId":"conv-main"}
{"display":"update them all","timestamp":2,"workspace":"/home/u/freshrss","conversationId":"conv-main"}
{"display":"fix the deploy","timestamp":3,"workspace":"/home/u/deploy","conversationId":"conv-other"}
{"display":"an id-less line from an old CLI","timestamp":4,"workspace":"/home/u/old"}
`)
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
}

func TestResolveRankAgy(t *testing.T) {
	roots := testRoot(t)
	p := New()
	ctx := context.Background()

	src, err := p.ResolveRank(ctx, roots, session.ListOptions{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "conv-other" {
		t.Errorf("rank 3 = %s, want conv-other (oldest)", src.Ref.SessionID)
	}
	if _, err := p.ResolveRank(ctx, roots, session.ListOptions{}, 9); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("want out-of-range error, got %v", err)
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
