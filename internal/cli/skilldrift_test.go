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

func TestInstalledSkillVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")

	if _, ok := installedSkillVersion(path); ok {
		t.Error("missing file reported as installed")
	}

	if err := os.WriteFile(path, stampSkillVersion([]byte(skillFixture), "v0.3.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, ok := installedSkillVersion(path)
	if !ok || v != "v0.3.0" {
		t.Errorf("got (%q, %v), want (v0.3.0, true)", v, ok)
	}

	if err := os.WriteFile(path, []byte(skillFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	v, ok = installedSkillVersion(path)
	if !ok || v != "" {
		t.Errorf("unstamped copy: got (%q, %v), want (\"\", true)", v, ok)
	}
}

func TestWarnSkillDrift(t *testing.T) {
	install := func(t *testing.T, version string) map[string]string {
		t.Helper()
		dir := t.TempDir()
		path := filepath.Join(dir, "catchup", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		md := []byte(skillFixture)
		if version != "" {
			md = stampSkillVersion(md, version)
		}
		if err := os.WriteFile(path, md, 0o644); err != nil {
			t.Fatal(err)
		}
		return map[string]string{session.ProviderClaude: dir}
	}

	t.Run("mismatch warns with fix", func(t *testing.T) {
		var errOut bytes.Buffer
		warnSkillDrift(install(t, "v0.1.0"), "v0.2.0", &errOut)
		for _, want := range []string{"skill drift", "claude v0.1.0", "binary v0.2.0", "run: catchup install-skill"} {
			if !strings.Contains(errOut.String(), want) {
				t.Errorf("warning missing %q: %s", want, errOut.String())
			}
		}
	})

	t.Run("unstamped copy warns", func(t *testing.T) {
		var errOut bytes.Buffer
		warnSkillDrift(install(t, ""), "v0.2.0", &errOut)
		if !strings.Contains(errOut.String(), "claude unstamped") {
			t.Errorf("want unstamped drift warning, got: %s", errOut.String())
		}
	})

	t.Run("match is silent", func(t *testing.T) {
		var errOut bytes.Buffer
		warnSkillDrift(install(t, "v0.2.0"), "v0.2.0", &errOut)
		if errOut.Len() != 0 {
			t.Errorf("unexpected warning: %s", errOut.String())
		}
	})

	t.Run("absent skill is a choice, not drift", func(t *testing.T) {
		var errOut bytes.Buffer
		warnSkillDrift(map[string]string{session.ProviderClaude: t.TempDir()}, "v0.2.0", &errOut)
		if errOut.Len() != 0 {
			t.Errorf("unexpected warning: %s", errOut.String())
		}
	})

	t.Run("dev build skips the check", func(t *testing.T) {
		var errOut bytes.Buffer
		warnSkillDrift(install(t, "v0.1.0"), "dev", &errOut)
		if errOut.Len() != 0 {
			t.Errorf("unexpected warning: %s", errOut.String())
		}
	})
}

// End to end: install-skill stamps the copy, and a later run under a newer
// binary surfaces the drift on stderr while stdout stays clean.
func TestRunWarnsOnSkillDrift(t *testing.T) {
	dir := t.TempDir()
	skillDirs := map[string]string{session.ProviderCodex: dir}

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"install-skill", "codex"}, session.Roots{}, nil, skillDirs, []byte(skillFixture), "v0.1.0", "", nil, &out, &errOut); err != nil {
		t.Fatalf("install-skill: %v", err)
	}

	out.Reset()
	errOut.Reset()
	if err := Run(context.Background(), []string{"codex", "--list"}, codexRoot(t), nil, skillDirs, nil, "v0.2.0", "/home/u/src/proj", nil, &out, &errOut); err != nil {
		t.Fatalf("list: %v (stderr: %s)", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "skill drift") {
		t.Errorf("want drift warning on stderr, got: %s", errOut.String())
	}
	if strings.Contains(out.String(), "skill drift") {
		t.Errorf("drift warning leaked into stdout:\n%s", out.String())
	}

	errOut.Reset()
	if err := Run(context.Background(), []string{"codex", "--list"}, codexRoot(t), nil, skillDirs, nil, "v0.1.0", "/home/u/src/proj", nil, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if errOut.Len() != 0 {
		t.Errorf("matching versions must be silent, got: %s", errOut.String())
	}
}
