package amp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wilbeibi/catchup/internal/providercheck"
	"github.com/wilbeibi/catchup/internal/session"
)

// thread mirrors a locally cached Amp thread: id, title, messages with a
// sentAt stamp, and a file tree uri carrying the working directory.
const thread = `{
  "id": "T-01a0be48",
  "title": "support amp",
  "messages": [
    {"role": "user", "content": "support amp", "meta": {"sentAt": 1786870675123}},
    {"role": "assistant", "content": [{"type": "text", "text": "on it"}], "meta": {"sentAt": 1786870676134}}
  ],
  "env": {"initial": {"trees": [{"uri": "file:///home/u/src/proj"}]}}
}`

func writeThread(t *testing.T, root, id, body string) {
	t.Helper()
	dir := filepath.Join(root, "threads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadAmpThread(t *testing.T) {
	root := t.TempDir()
	writeThread(t, root, "T-01a0be48", thread)

	p := New()
	src, err := p.Resolve(context.Background(), session.Roots{Amp: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "T-01a0be48" || src.Metadata["title"] != "support amp" {
		t.Fatalf("source = %+v", src)
	}
	if src.Metadata["cwd"] != "/home/u/src/proj" {
		t.Errorf("cwd = %q", src.Metadata["cwd"])
	}
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Entries) != 2 {
		t.Fatalf("entries = %+v", th.Entries)
	}
	if th.Entries[0].Time.IsZero() {
		t.Error("user entry lost its sentAt timestamp")
	}

	providercheck.Check(t, p, session.Roots{Amp: root}, providercheck.Expect{Timestamps: true})
}
