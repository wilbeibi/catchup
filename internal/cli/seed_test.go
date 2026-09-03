package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteSeedFile(t *testing.T) {
	dir := t.TempDir()
	body := "# transcript\nline two\n"
	rel, err := writeSeedFile(dir, "codex-sess-1", body)
	if err != nil {
		t.Fatal(err)
	}

	// The path is relative because the agent starts in dir, and it names the
	// source so a directory of kept seeds stays browsable.
	if filepath.IsAbs(rel) {
		t.Errorf("seed path %q is absolute; the agent is handed a workspace-relative one", rel)
	}
	if !strings.HasPrefix(rel, seedDirName) || !strings.Contains(rel, "codex-sess-1") {
		t.Errorf("seed path = %q, want %s/seed-<stamp>-codex-sess-1.md", rel, seedDirName)
	}

	got, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("seed file = %q, want the body verbatim", got)
	}

	// The directory hides itself rather than catchup editing a .gitignore
	// the user owns.
	ignore, err := os.ReadFile(filepath.Join(dir, seedDirName, ".gitignore"))
	if err != nil {
		t.Fatalf("seed dir does not ignore itself: %v", err)
	}
	if strings.TrimSpace(string(ignore)) != "*" {
		t.Errorf(".gitignore = %q, want *", ignore)
	}
}

// The channel is the platform's, so this asserts whichever one this build
// carries (docs/DESIGN.md, D6b): inline in argv on Unix, a file beside the
// agent on Windows.
func TestSeedPromptChannel(t *testing.T) {
	dir := t.TempDir()
	body := "TRANSCRIPT\nsecond line"
	prompt, err := seedPrompt(seed{
		inlineLead: "LEAD",
		fileLead:   "read %s first",
		body:       body,
		label:      "codex-sess-1",
		dir:        dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		if prompt != "LEAD\n\n"+body {
			t.Errorf("prompt = %q, want the body inline after the lead", prompt)
		}
		if _, err := os.Stat(filepath.Join(dir, seedDirName)); err == nil {
			t.Error("Unix wrote a seed file; the transcript belongs in argv there")
		}
		return
	}

	// Windows: argv carries a pointer, never the transcript — cmd.exe would
	// cut it at the first newline without saying so.
	if strings.Contains(prompt, body) {
		t.Errorf("prompt inlines the transcript on Windows: %q", prompt)
	}
	if strings.ContainsAny(prompt, "\n") {
		t.Errorf("prompt is multi-line on Windows, so a .cmd shim would truncate it: %q", prompt)
	}
	rel := strings.TrimSuffix(strings.TrimPrefix(prompt, "read "), " first")
	got, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatalf("prompt %q does not name a readable seed file: %v", prompt, err)
	}
	if string(got) != body {
		t.Errorf("seed file = %q, want the body verbatim", got)
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"codex-sess-1", "codex-sess-1"},
		{"https://box.ts.net/s.md?sig=x", "https-box-ts-net-s-md-sig-x"},
		{"/home/u/handoff.md", "home-u-handoff-md"},
		{"stdin", "stdin"},
		{"...", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := slug(tt.in); got != tt.want {
			t.Errorf("slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if n := len(slug(strings.Repeat("a", 200))); n > 48 {
		t.Errorf("slug length %d, want a bounded file name", n)
	}
}
