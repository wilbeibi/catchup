package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wilbeibi/baton/internal/session"
)

func writeTranscript(t *testing.T, root, project, uuid, body string, mod time.Time) string {
	t.Helper()
	dir := filepath.Join(root, "projects", project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, uuid+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

const transcript = `{"type":"ai-title","aiTitle":"Wire up baton","sessionId":"sess-a"}
{"type":"user","sessionId":"sess-a","cwd":"/home/u/src/baton","gitBranch":"main","timestamp":"2026-06-26T10:00:00Z","isMeta":true,"message":{"role":"user","content":"<command-stdout>injected</command-stdout>"}}
{"type":"user","sessionId":"sess-a","cwd":"/home/u/src/baton","timestamp":"2026-06-26T10:01:00Z","message":{"role":"user","content":"implement the parser"}}
{"type":"user","sessionId":"sess-a","isSidechain":true,"timestamp":"2026-06-26T10:01:30Z","message":{"role":"user","content":"subagent noise"}}
{"type":"assistant","sessionId":"sess-a","timestamp":"2026-06-26T10:02:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"x"},{"type":"text","text":"on it"},{"type":"tool_use","name":"Edit","input":{}}]}}
{"type":"user","sessionId":"sess-a","isCompactSummary":true,"timestamp":"2026-06-26T10:03:00Z","message":{"role":"user","content":"summary so far"}}
`

func TestReadThread(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "-home-u-src-baton", "sess-a", transcript, time.Now())

	p := New()
	roots := session.Roots{Claude: root}
	src, err := p.Resolve(context.Background(), roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Metadata["title"] != "Wire up baton" {
		t.Errorf("title = %q", src.Metadata["title"])
	}
	if src.Metadata["branch"] != "main" {
		t.Errorf("branch = %q", src.Metadata["branch"])
	}

	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ kind, role, text string }{
		{session.KindMessage, session.RoleUser, "implement the parser"},
		{session.KindMessage, session.RoleAssistant, "on it"}, // thinking + tool_use dropped
		{session.KindCompact, "", "summary so far"},
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

func TestListSkipsSubagents(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "-proj", "main-1", transcript, time.Now())
	writeTranscript(t, root, filepath.Join("-proj", "subagents"), "sub-1", transcript, time.Now())

	p := New()
	sums, err := p.List(context.Background(), session.Roots{Claude: root}, session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 {
		t.Fatalf("got %d summaries, want 1 (subagents skipped): %+v", len(sums), sums)
	}
	if sums[0].Preview != "implement the parser" {
		t.Errorf("preview = %q", sums[0].Preview)
	}
}
