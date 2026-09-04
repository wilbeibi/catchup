package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// Two forks of the same source inside one second want the same file name, and
// the first agent is already reading the file it was handed, so the second must
// take a new name rather than truncate it.
func TestWriteSeedFileNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	frozen := time.Date(2026, 9, 3, 11, 22, 33, 0, time.UTC)
	seedNow = func() time.Time { return frozen }
	t.Cleanup(func() { seedNow = time.Now })

	bodies := []string{"first body\n", "second body\n", "third body\n"}
	rels := make([]string, 0, len(bodies))
	for _, body := range bodies {
		rel, err := writeSeedFile(dir, "codex-sess-1", body)
		if err != nil {
			t.Fatal(err)
		}
		rels = append(rels, rel)
	}

	seen := map[string]bool{}
	for i, rel := range rels {
		if seen[rel] {
			t.Fatalf("seed %d reused path %q; an earlier agent's briefing was overwritten", i, rel)
		}
		seen[rel] = true
		// The returned path has to name the file that was written, since it is
		// the only pointer the launched agent gets.
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("returned path %q is not readable: %v", rel, err)
		}
		if string(got) != bodies[i] {
			t.Errorf("seed %s = %q, want %q", rel, got, bodies[i])
		}
	}

	// The stamp is the primary form; only the collisions carry a counter.
	stamp := seedDirName + string(filepath.Separator) + "seed-20260903-112233-codex-sess-1"
	want := []string{stamp + ".md", stamp + "-2.md", stamp + "-3.md"}
	for i, w := range want {
		if rels[i] != w {
			t.Errorf("seed %d path = %q, want %q", i, rels[i], w)
		}
	}
	// The suffixed name still has to look like a seed to the prompt scanner.
	for _, rel := range rels {
		if !seedPathRE.MatchString(rel) {
			t.Errorf("seed path %q is not recognised as one", rel)
		}
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
