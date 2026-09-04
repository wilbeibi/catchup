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
	rel, err := writeSeedFile(dir, body)
	if err != nil {
		t.Fatal(err)
	}

	// The path is relative because the agent starts in dir.
	if filepath.IsAbs(rel) {
		t.Errorf("seed path %q is absolute; the agent is handed a workspace-relative one", rel)
	}
	if !seedPathRE.MatchString(rel) {
		t.Errorf("seed path = %q, want %s/seed-<digest>.md", rel, seedDirName)
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

// Identical handoffs share one immutable file instead of retaining another
// complete transcript for every launch.
func TestWriteSeedFileReusesIdenticalBody(t *testing.T) {
	dir := t.TempDir()
	body := "same transcript\n"
	first, err := writeSeedFile(dir, body)
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeSeedFile(dir, body)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("identical bodies used %q and %q; want one retained seed", first, second)
	}
	entries, err := os.ReadDir(filepath.Join(dir, seedDirName))
	if err != nil {
		t.Fatal(err)
	}
	var markdown int
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".md" {
			markdown++
		}
	}
	if markdown != 1 {
		t.Errorf("seed directory has %d transcript files, want 1", markdown)
	}
}

// Different handoffs remain separate immutable files, and each returned path
// continues to name the body given to that call.
func TestWriteSeedFileKeepsDifferentBodies(t *testing.T) {
	dir := t.TempDir()
	bodies := []string{"first body\n", "second body\n"}
	rels := make([]string, len(bodies))
	for i, body := range bodies {
		var err error
		rels[i], err = writeSeedFile(dir, body)
		if err != nil {
			t.Fatal(err)
		}
	}
	if rels[0] == rels[1] {
		t.Fatalf("different bodies reused %q", rels[0])
	}
	for i, rel := range rels {
		got, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != bodies[i] {
			t.Errorf("seed %q = %q, want %q", rel, got, bodies[i])
		}
	}
}

// The channel is the platform's: inline in argv on Unix, a file beside the
// agent on Windows.
func TestSeedPromptChannel(t *testing.T) {
	dir := t.TempDir()
	body := "TRANSCRIPT\nsecond line"
	prompt, err := seedPrompt(seed{
		inlineLead: "LEAD",
		fileLead:   "read %s first",
		body:       body,
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
