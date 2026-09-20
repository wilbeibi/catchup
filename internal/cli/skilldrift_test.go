package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wilbeibi/catchup/internal/session"
)

const skillFixture = "---\nname: catchup\ndescription: test skill\n---\n\n# catchup\n"

func TestStampSkillVersion(t *testing.T) {
	stamped := string(stampSkillVersion([]byte(skillFixture), "v0.1.0"))
	for _, want := range []string{"version: v0.1.0\n", "name: catchup\n", "# catchup\n"} {
		if !strings.Contains(stamped, want) {
			t.Errorf("stamped skill missing %q:\n%s", want, stamped)
		}
	}

	restamped := string(stampSkillVersion([]byte(stamped), "v0.2.0"))
	if strings.Contains(restamped, "v0.1.0") {
		t.Errorf("re-stamp kept the old version:\n%s", restamped)
	}
	if got := strings.Count(restamped, "version:"); got != 1 {
		t.Errorf("re-stamp left %d version lines, want 1:\n%s", got, restamped)
	}

	plain := "# no frontmatter\n"
	if got := string(stampSkillVersion([]byte(plain), "v0.1.0")); got != plain {
		t.Errorf("content without frontmatter changed: %q", got)
	}
}

func TestSkillVersion(t *testing.T) {
	if v := skillVersion(stampSkillVersion([]byte(skillFixture), "v0.3.0")); v != "v0.3.0" {
		t.Errorf("stamped copy: got %q, want v0.3.0", v)
	}
	if v := skillVersion([]byte(skillFixture)); v != "" {
		t.Errorf("unstamped copy: got %q, want \"\"", v)
	}
	if v := skillVersion([]byte("# no frontmatter\n")); v != "" {
		t.Errorf("copy without frontmatter: got %q, want \"\"", v)
	}
}

// skillBody must see through the stamp and the line endings, and nothing else.
func TestSkillBody(t *testing.T) {
	want := string(skillBody([]byte(skillFixture)))
	tests := []struct {
		name string
		md   string
		same bool
	}{
		{"unstamped", skillFixture, true},
		{"stamped", string(stampSkillVersion([]byte(skillFixture), "v0.9.0")), true},
		{"restamped", string(stampSkillVersion(stampSkillVersion([]byte(skillFixture), "v0.1.0"), "v0.9.0")), true},
		{"crlf", strings.ReplaceAll(skillFixture, "\n", "\r\n"), true},
		{"edited body", strings.Replace(skillFixture, "# catchup\n", "# catchup, edited\n", 1), false},
		{"edited frontmatter", strings.Replace(skillFixture, "test skill", "other skill", 1), false},
		{"no frontmatter", "# catchup\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(skillBody([]byte(tt.md))) == want; got != tt.same {
				t.Errorf("skillBody equal = %v, want %v:\n%s", got, tt.same, skillBody([]byte(tt.md)))
			}
		})
	}
}

// installSkillCopy writes content into a fresh skill directory, stamped with
// version unless version is "". It returns the skillDirs entry and the file.
func installSkillCopy(t *testing.T, content, version string) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "catchup", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	md := []byte(content)
	if version != "" {
		md = stampSkillVersion(md, version)
	}
	if err := os.WriteFile(path, md, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

func TestWarnSkillDrift(t *testing.T) {
	const binary = "v0.2.0"
	edited := strings.Replace(skillFixture, "# catchup\n", "# catchup, hand edited\n", 1)
	older := strings.Replace(skillFixture, "# catchup\n", "# catchup, as of last release\n", 1)

	tests := []struct {
		name    string
		absent  bool   // install nothing: the directory exists, the copy does not
		content string // what was installed, before stamping
		stamp   string // the version it was stamped with; "" leaves it unstamped
		version string // the running binary's version
		want    string // a substring of the expected warning; "" means silent
	}{
		{name: "same content, same stamp", content: skillFixture, stamp: binary, version: binary},
		{name: "same content, older stamp", content: skillFixture, stamp: "v0.1.0", version: binary},
		{name: "same content, newer stamp", content: skillFixture, stamp: "v0.3.0", version: binary},
		{name: "same content, no stamp", content: skillFixture, version: binary},
		{
			name: "line endings alone are not drift", content: strings.ReplaceAll(skillFixture, "\n", "\r\n"),
			stamp: "v0.1.0", version: binary,
		},
		{name: "older content warns", content: older, stamp: "v0.1.0", version: binary, want: "claude v0.1.0"},
		{name: "older unstamped content warns", content: older, version: binary, want: "claude unstamped"},
		{name: "hand-edited copy keeps its stamp and is left alone", content: edited, stamp: binary, version: binary},
		{name: "absent copy is a choice, not drift", absent: true, version: binary},
		{name: "dev build skips the check", content: older, stamp: "v0.1.0", version: "dev"},
		{name: "unversioned build skips the check", content: older, stamp: "v0.1.0", version: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if !tt.absent {
				dir, _ = installSkillCopy(t, tt.content, tt.stamp)
			}
			var errOut bytes.Buffer
			warnSkillDrift(map[string]string{session.ProviderClaude: dir}, []byte(skillFixture), tt.version, &errOut)
			switch {
			case tt.want == "" && errOut.Len() != 0:
				t.Errorf("want silence, got: %s", errOut.String())
			case tt.want != "" && !strings.Contains(errOut.String(), tt.want):
				t.Errorf("want warning containing %q, got: %s", tt.want, errOut.String())
			}
		})
	}
}

func TestWarnSkillDriftMessage(t *testing.T) {
	older := strings.Replace(skillFixture, "# catchup\n", "# catchup, as of last release\n", 1)
	dir, _ := installSkillCopy(t, older, "v0.1.0")

	var errOut bytes.Buffer
	warnSkillDrift(map[string]string{session.ProviderClaude: dir}, []byte(skillFixture), "v0.2.0", &errOut)
	for _, want := range []string{"skill drift", "claude v0.1.0", "binary v0.2.0", "run: catchup install-skill"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("warning missing %q: %s", want, errOut.String())
		}
	}
}

// An unreadable copy is not evidence of drift: the check is read-only and has
// nothing to compare, so it says nothing rather than guessing.
func TestWarnSkillDriftUnreadableCopy(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	dir, path := installSkillCopy(t, skillFixture, "v0.1.0")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	var errOut bytes.Buffer
	warnSkillDrift(map[string]string{session.ProviderClaude: dir}, []byte(skillFixture), "v0.2.0", &errOut)
	if errOut.Len() != 0 {
		t.Errorf("want silence, got: %s", errOut.String())
	}
}

// Every agent gets its own copy, and they age independently: the warning
// names the ones that actually differ and stays quiet about the rest.
func TestWarnSkillDriftMixedAgents(t *testing.T) {
	older := strings.Replace(skillFixture, "# catchup\n", "# catchup, as of last release\n", 1)
	current, _ := installSkillCopy(t, skillFixture, "v0.1.0")
	stale, _ := installSkillCopy(t, older, "v0.1.0")
	unstamped, _ := installSkillCopy(t, older, "")
	skillDirs := map[string]string{
		session.ProviderClaude: current,
		session.ProviderCodex:  stale,
		session.ProviderCursor: unstamped,
		session.ProviderKimi:   t.TempDir(), // never installed
	}

	var errOut bytes.Buffer
	warnSkillDrift(skillDirs, []byte(skillFixture), "v0.2.0", &errOut)
	for _, want := range []string{"codex v0.1.0", "cursor unstamped"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("warning missing %q: %s", want, errOut.String())
		}
	}
	for _, absent := range []string{"claude", "kimi"} {
		if strings.Contains(errOut.String(), absent) {
			t.Errorf("warning names %s, which did not drift: %s", absent, errOut.String())
		}
	}
	if got := strings.Count(errOut.String(), "\n"); got != 1 {
		t.Errorf("want one warning line, got %d:\n%s", got, errOut.String())
	}
}

// End to end: install-skill stamps the copy, a later run whose skill reads
// differently surfaces the drift on stderr while stdout stays clean, and a
// release that only moved the version number says nothing.
func TestRunWarnsOnSkillDrift(t *testing.T) {
	dir := t.TempDir()
	skillDirs := map[string]string{session.ProviderCodex: dir}
	rewritten := []byte(strings.Replace(skillFixture, "# catchup\n", "# catchup, with a new flag\n", 1))

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"install-skill", "codex"}, session.Roots{}, nil, skillDirs, []byte(skillFixture), "v0.1.0", "", nil, &out, &errOut); err != nil {
		t.Fatalf("install-skill: %v", err)
	}

	out.Reset()
	errOut.Reset()
	if err := Run(context.Background(), []string{"codex", "--list"}, codexRoot(t), nil, skillDirs, rewritten, "v0.2.0", "/home/u/src/proj", nil, &out, &errOut); err != nil {
		t.Fatalf("list: %v (stderr: %s)", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "skill drift") {
		t.Errorf("want drift warning on stderr, got: %s", errOut.String())
	}
	if strings.Contains(out.String(), "skill drift") {
		t.Errorf("drift warning leaked into stdout:\n%s", out.String())
	}

	errOut.Reset()
	if err := Run(context.Background(), []string{"codex", "--list"}, codexRoot(t), nil, skillDirs, []byte(skillFixture), "v0.2.0", "/home/u/src/proj", nil, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if errOut.Len() != 0 {
		t.Errorf("a release that left SKILL.md alone must be silent, got: %s", errOut.String())
	}
}
