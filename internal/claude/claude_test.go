package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
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

const transcript = `{"type":"ai-title","aiTitle":"Wire up catchup","sessionId":"sess-a"}
{"type":"user","sessionId":"sess-a","cwd":"/home/u/src/catchup","gitBranch":"main","timestamp":"2026-06-26T10:00:00Z","isMeta":true,"message":{"role":"user","content":"<command-stdout>injected</command-stdout>"}}
{"type":"user","sessionId":"sess-a","cwd":"/home/u/src/catchup","timestamp":"2026-06-26T10:01:00Z","message":{"role":"user","content":"implement the parser"}}
{"type":"user","sessionId":"sess-a","isSidechain":true,"timestamp":"2026-06-26T10:01:30Z","message":{"role":"user","content":"subagent noise"}}
{"type":"assistant","sessionId":"sess-a","timestamp":"2026-06-26T10:02:00Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"x"},{"type":"text","text":"on it"},{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"go test ./..."}}]}}
{"type":"user","sessionId":"sess-a","timestamp":"2026-06-26T10:02:10Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu1","content":"FAIL\tproj\nexit status 1","is_error":true}]}}
{"type":"assistant","sessionId":"sess-a","timestamp":"2026-06-26T10:02:20Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu2","name":"Read","input":{"file_path":"/x.go"}}]}}
{"type":"user","sessionId":"sess-a","timestamp":"2026-06-26T10:02:30Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu2","content":[{"type":"text","text":"tool output"}]}]}}
{"type":"user","sessionId":"sess-a","isCompactSummary":true,"timestamp":"2026-06-26T10:03:00Z","message":{"role":"user","content":"summary so far"}}
`

func TestReadThread(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "-home-u-src-catchup", "sess-a", transcript, time.Now())

	p := New()
	roots := session.Roots{Claude: root}
	src, err := p.Resolve(context.Background(), roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Metadata["title"] != "Wire up catchup" {
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
		{session.KindFailure, session.RoleTool, "FAIL\tproj\nexit status 1"},
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
	// The failure is paired with its tool_use for the name and input.
	if f := th.Entries[2]; f.Tool != "Bash" || f.Input != `{"command":"go test ./..."}` {
		t.Errorf("failure = %+v, want Tool Bash with structured command input", f)
	}
	// The successful Read never reaches the timeline.
	if strings.Contains(th.VisibleText(), "tool output") {
		t.Errorf("successful tool result leaked into the timeline: %+v", th.Entries)
	}
}

// A session that cd's away keeps the directory it was started in: the records
// below walk into a subdirectory, on to a sibling repo, and back, and the
// first cwd is the one a listing must find it under. The empty cwd ahead of
// them stands for a record that carries the field without a value.
const wanderer = `{"type":"user","sessionId":"sess-w","cwd":"","timestamp":"2026-06-26T10:00:00Z","message":{"role":"user","content":"start here"}}
{"type":"assistant","sessionId":"sess-w","cwd":"/home/u/src/catchup","timestamp":"2026-06-26T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}
{"type":"user","sessionId":"sess-w","cwd":"/home/u/src/catchup/internal","timestamp":"2026-06-26T10:01:00Z","message":{"role":"user","content":"now the subdirectory"}}
{"type":"user","sessionId":"sess-w","cwd":"/home/u/src/other","timestamp":"2026-06-26T10:02:00Z","message":{"role":"user","content":"now the sibling"}}
{"type":"assistant","sessionId":"sess-w","cwd":"/home/u/src/catchup/internal","timestamp":"2026-06-26T10:03:00Z","message":{"role":"assistant","content":[{"type":"text","text":"back in the subdirectory"}]}}
`

func TestCwdIsWhereTheSessionStarted(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "-home-u-src-catchup", "sess-w", wanderer, time.Now())

	p := New()
	roots := session.Roots{Claude: root}
	src, err := p.Resolve(context.Background(), roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := src.Metadata["cwd"]; got != "/home/u/src/catchup" {
		t.Errorf("cwd = %q, want the directory the session opened in", got)
	}

	// A listing run where the session started finds it; one run from the
	// directory it wandered into does not.
	for dir, want := range map[string]int{
		"/home/u/src/catchup":          1,
		"/home/u/src/catchup/internal": 0,
		"/home/u/src/other":            0,
	} {
		sums, err := p.List(context.Background(), roots, session.ListOptions{Cwd: dir})
		if err != nil {
			t.Fatal(err)
		}
		if len(sums) != want {
			t.Errorf("listing in %s returned %d sessions, want %d", dir, len(sums), want)
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
