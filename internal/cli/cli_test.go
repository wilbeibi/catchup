package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

const rollout = `{"timestamp":"2026-06-26T21:31:46.0Z","type":"session_meta","payload":{"id":"sess-1","cwd":"/home/u/src/proj","cli_version":"0.1"}}
{"timestamp":"2026-06-26T21:31:55.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello from cli test"}]}}
{"timestamp":"2026-06-26T21:32:08.0Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi back"}]}}
`

// codexRoot writes a single codex rollout fixture and returns a Roots pointing at
// it, exercising the real codex provider through cli.Run.
func codexRoot(t *testing.T) session.Roots {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "06", "26")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-sess-1.jsonl"), []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}
	return session.Roots{Codex: root}
}

func run(t *testing.T, roots session.Roots, args ...string) string {
	t.Helper()
	return runWithCwd(t, roots, "", args...)
}

// claudeRoot writes two Claude transcripts sharing one working directory and
// returns a Roots pointing at them. sess-new is given the newer mtime, so
// "newest in cwd" resolves to it; a test can then prove that an injected
// current-session id (sess-old) wins over recency.
func claudeRoot(t *testing.T) session.Roots {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id, text string, mod time.Time) {
		lines := fmt.Sprintf(
			`{"type":"user","sessionId":%q,"cwd":"/home/u/proj","timestamp":"2026-06-26T10:00:00Z","message":{"role":"user","content":%q}}
{"type":"assistant","sessionId":%q,"cwd":"/home/u/proj","timestamp":"2026-06-26T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}
`, id, text, id)
		p := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	write("sess-old", "old session question", now.Add(-time.Hour))
	write("sess-new", "new session question", now)
	return session.Roots{Claude: root}
}

func runWithCwd(t *testing.T, roots session.Roots, cwd string, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), args, roots, nil, cwd, &out, &errOut); err != nil {
		t.Fatalf("Run(%v) error: %v (stderr: %s)", args, err, errOut.String())
	}
	return out.String()
}

func TestRunCwdFiltering(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "06", "26")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	catchupRollout := `{"timestamp":"2026-06-26T21:31:46.0Z","type":"session_meta","payload":{"id":"sess-catchup","cwd":"/home/u/src/catchup","cli_version":"0.1"}}
{"timestamp":"2026-06-26T21:31:55.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"refactor the auth module"}]}}
{"timestamp":"2026-06-26T21:32:08.0Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}
`
	otherRollout := `{"timestamp":"2026-06-26T22:00:00.0Z","type":"session_meta","payload":{"id":"sess-other","cwd":"/home/u/src/other","cli_version":"0.1"}}
{"timestamp":"2026-06-26T22:01:00.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"unrelated work"}]}}
{"timestamp":"2026-06-26T22:02:00.0Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "rollout-sess-catchup.jsonl"), []byte(catchupRollout), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-sess-other.jsonl"), []byte(otherRollout), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := session.Roots{Codex: root}

	// With cwd: only the matching session appears
	out := runWithCwd(t, roots, "/home/u/src/catchup", "codex", "--list")
	if !strings.Contains(out, "sess-catchup") {
		t.Errorf("expected sess-catchup in cwd-filtered listing, got:\n%s", out)
	}
	if strings.Contains(out, "sess-other") {
		t.Errorf("unexpected sess-other in cwd-filtered listing, got:\n%s", out)
	}
}

func TestRunRendersLatestMarkdown(t *testing.T) {
	out := run(t, codexRoot(t), "codex")
	for _, want := range []string{"provider: codex", "session: sess-1", "## 1. user", "hello from cli test", "## 2. assistant", "hi back"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestLastTurns(t *testing.T) {
	// Keeping the last turn must start at the final user message and include
	// every assistant reply that follows it — not the prior exchange or the
	// compaction marker before it.
	u := func(s string) session.Entry { return session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: s} }
	a := func(s string) session.Entry { return session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: s} }
	compact := session.Entry{Kind: session.KindCompact}

	thread := session.Thread{Entries: []session.Entry{
		u("q1"), a("a1"),
		compact,
		u("q2"), a("a2a"), a("a2b"),
	}}

	got := lastTurns(thread, 1)
	want := []string{"q2", "a2a", "a2b"}
	if len(got.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got.Entries), len(want))
	}
	for i, e := range got.Entries {
		if e.Text != want[i] {
			t.Errorf("entry %d = %q, want %q", i, e.Text, want[i])
		}
	}
}

func TestSinceCompact(t *testing.T) {
	u := func(s string) session.Entry { return session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: s} }
	a := func(s string) session.Entry { return session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: s} }
	summary := session.Entry{Kind: session.KindCompact, Text: "recap so far"}

	// Keep the last compaction entry (the recap) and everything after it.
	thread := session.Thread{Entries: []session.Entry{
		u("q1"), a("a1"),
		summary,
		u("q2"), a("a2"),
	}}
	got := sinceCompact(thread)
	want := []string{"recap so far", "q2", "a2"}
	if len(got.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got.Entries), len(want))
	}
	for i, e := range got.Entries {
		if e.Text != want[i] {
			t.Errorf("entry %d = %q, want %q", i, e.Text, want[i])
		}
	}

	// No compaction marker: the whole thread is returned unchanged.
	plain := session.Thread{Entries: []session.Entry{u("q1"), a("a1")}}
	if got := sinceCompact(plain); len(got.Entries) != 2 {
		t.Errorf("no-compaction: got %d entries, want 2", len(got.Entries))
	}
}

func TestCurrentSessionBeatsNewest(t *testing.T) {
	roots := claudeRoot(t)
	cwd := "/home/u/proj"

	// With no injected current id, the default selection is the newest session
	// in cwd.
	out := runWithCwd(t, roots, cwd, "claude")
	if !strings.Contains(out, "session: sess-new") {
		t.Fatalf("default should resolve newest-in-cwd, got:\n%s", out)
	}

	// Claude injects the live session's id; it must win over recency so the
	// right one of several sessions sharing a directory is picked.
	var got, errOut bytes.Buffer
	current := map[string]string{session.ProviderClaude: "sess-old"}
	if err := Run(context.Background(), []string{"claude"}, roots, current, cwd, &got, &errOut); err != nil {
		t.Fatalf("Run error: %v (stderr: %s)", err, errOut.String())
	}
	if !strings.Contains(got.String(), "session: sess-old") {
		t.Errorf("injected current id should win over newest, got:\n%s", got.String())
	}
	if strings.Contains(got.String(), "sess-new") {
		t.Errorf("newest session leaked despite injected current id:\n%s", got.String())
	}
}

func TestRunUnknownProvider(t *testing.T) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"bogus"}, session.Roots{}, nil, "", &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("expected unknown provider error, got %v", err)
	}
}
