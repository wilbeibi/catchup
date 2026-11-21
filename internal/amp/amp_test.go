package amp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

func TestReadAndListEdgeCases(t *testing.T) {
	root := t.TempDir()
	threads := filepath.Join(root, "threads")
	if err := os.Mkdir(threads, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(name, body string, mod time.Time) {
		t.Helper()
		p := filepath.Join(threads, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	// Windows file URI after a remote URI that must be skipped, string content,
	// array of strings + typed blocks, and non-child handoff ignored.
	write("T-win.json", `{
		"id": "T-win",
		"title": "Windows thread",
		"env": {"initial": {"trees": [
			{"uri": "file://remote-host/share"},
			{"uri": "file:///C:/Users/dev/my%20repo"}
		]}},
		"relationships": [
			{"type": "handoff", "role": "parent", "threadID": "T-child"},
			{"type": "handoff", "role": "child", "threadID": "T-parent"}
		],
		"messages": [
			{"role": "user", "content": "plain string prompt", "meta": {"sentAt": 1771304680667}},
			{"role": "assistant", "content": ["line one", {"type": "thinking", "text": "hidden"}, {"type": "text", "text": "line two"}]}
		]
	}`, now)
	write("broken.json", `{not json`, now.Add(time.Minute))
	write("no-id.json", `{"title":"orphan"}`, now.Add(2*time.Minute))

	p := New()
	roots := session.Roots{Amp: root}

	sums, err := p.List(context.Background(), roots, session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "T-win" {
		t.Fatalf("List = %+v, want single T-win", sums)
	}

	src, err := p.Resolve(context.Background(), roots, "T-win")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := src.Metadata["cwd"], filepath.FromSlash("C:/Users/dev/my repo"); got != want {
		t.Errorf("cwd = %q, want %q", got, want)
	}
	if src.Metadata["parent"] != "T-parent" || src.Metadata["relationship"] != "handoff" {
		t.Errorf("relationship metadata = %+v", src.Metadata)
	}

	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Entries) != 2 || th.Entries[0].Text != "plain string prompt" || th.Entries[1].Text != "line one\nline two" {
		t.Fatalf("entries = %+v", th.Entries)
	}
	if th.Entries[0].Time.UnixMilli() != 1771304680667 {
		t.Errorf("sentAt time = %v", th.Entries[0].Time)
	}

	if _, err := p.Read(context.Background(), session.Source{Path: filepath.Join(threads, "no-id.json")}); err == nil || !strings.Contains(err.Error(), "no thread id") {
		t.Errorf("missing id Read error = %v", err)
	}
	if _, err := p.Resolve(context.Background(), roots, "T-missing"); err == nil {
		t.Fatal("Resolve of missing id succeeded")
	}
}
