package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

// writeRollout creates a rollout file under <root>/sessions/Y/M/D and stamps its
// modification time so recency ordering is deterministic.
func writeRollout(t *testing.T, root, name, body string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(root, "sessions", "2026", "06", "26")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

const rolloutOne = `{"timestamp":"2026-06-26T21:31:46.0Z","type":"session_meta","payload":{"id":"sess-1","cwd":"/home/u/src/proj","cli_version":"0.139.0","model_provider":"openai"}}
{"timestamp":"2026-06-26T21:31:50.0Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions>"}]}}
{"timestamp":"2026-06-26T21:31:52.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /home/u/src/proj\n\n<INSTRUCTIONS>\nbe nice\n</INSTRUCTIONS>"}]}}
{"timestamp":"2026-06-26T21:31:55.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello codex"}]}}
{"timestamp":"2026-06-26T21:32:00.0Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{}"}}
{"timestamp":"2026-06-26T21:32:05.0Z","type":"response_item","payload":{"type":"reasoning","summary":[]}}
{"timestamp":"2026-06-26T21:32:08.0Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi human"}]}}
{"timestamp":"2026-06-26T21:32:10.0Z","type":"event_msg","payload":{"type":"context_compacted"}}
`

func TestReadThread(t *testing.T) {
	root := t.TempDir()
	writeRollout(t, root, "rollout-2026-06-26T21-31-46-sess-1.jsonl", rolloutOne, time.Now())

	p := New()
	roots := session.Roots{Codex: root}
	src, err := p.Resolve(context.Background(), roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "sess-1" {
		t.Fatalf("session id = %q, want sess-1", src.Ref.SessionID)
	}
	if src.Metadata["title"] != "proj" || src.Metadata["cwd"] != "/home/u/src/proj" {
		t.Errorf("metadata = %v", src.Metadata)
	}

	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	// developer role, the AGENTS.md injection, function_call and reasoning are
	// all dropped; only the typed turns and the compaction marker remain.
	want := []struct{ kind, role, text string }{
		{session.KindMessage, session.RoleUser, "hello codex"},
		{session.KindMessage, session.RoleAssistant, "hi human"},
		{session.KindCompact, "", ""},
	}
	if len(th.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(th.Entries), len(want), th.Entries)
	}
	for i, w := range want {
		got := th.Entries[i]
		if got.Kind != w.kind || got.Role != w.role || got.Text != w.text {
			t.Errorf("entry %d = %+v, want %v", i, got, w)
		}
	}
}

func TestListRecencyAndResolveByID(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-time.Hour)
	writeRollout(t, root, "rollout-old-sess-1.jsonl",
		`{"type":"session_meta","payload":{"id":"sess-1","cwd":"/a/old"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]}}
`, old)
	writeRollout(t, root, "rollout-new-sess-2.jsonl",
		`{"type":"session_meta","payload":{"id":"sess-2","cwd":"/a/new"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second"}]}}
`, time.Now())

	p := New()
	roots := session.Roots{Codex: root}

	sums, err := p.List(context.Background(), roots, session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 {
		t.Fatalf("got %d summaries, want 2", len(sums))
	}
	if sums[0].Rank != 1 || sums[0].Ref.SessionID != "sess-2" {
		t.Errorf("newest should rank 1: %+v", sums[0])
	}
	if sums[0].Preview != "second" {
		t.Errorf("preview = %q, want second", sums[0].Preview)
	}

	// Explicit id resolves regardless of recency.
	src, err := p.Resolve(context.Background(), roots, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if src.Metadata["cwd"] != "/a/old" {
		t.Errorf("resolved wrong session: %v", src.Metadata)
	}
}
