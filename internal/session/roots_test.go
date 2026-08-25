package session

import (
	"path/filepath"
	"testing"
)

// allProviders is every provider name the registry knows. It is spelled out
// here rather than imported from the cli layer (which would invert the
// dependency) so that adding a provider without extending root resolution
// fails loudly.
var allProviders = []string{
	ProviderCodex, ProviderClaude, ProviderAgy, ProviderOpenCode,
	ProviderPiAgent, ProviderKimi, ProviderCline, ProviderCursor,
	ProviderZCode, ProviderDeepSeek, ProviderCopilot,
}

// noEnv is the environment of a machine that overrides nothing.
func noEnv(string) string { return "" }

// envFrom serves a fixed map, so a case can override one variable without
// disturbing the rest.
func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// Expected paths are built with filepath.Join against a real temp home rather
// than written as literals, so the assertions state "this subpath under home"
// on every platform instead of baking in a separator.
func TestResolveRootsDefaults(t *testing.T) {
	home := t.TempDir()
	got := ResolveRoots(noEnv, home)
	want := Roots{
		Codex:    filepath.Join(home, ".codex"),
		Claude:   filepath.Join(home, ".claude"),
		Agy:      filepath.Join(home, ".gemini", "antigravity-cli"),
		OpenCode: filepath.Join(home, ".local", "share", "opencode"),
		PiAgent:  filepath.Join(home, ".pi", "agent"),
		Kimi:     filepath.Join(home, ".kimi-code"),
		Cline:    filepath.Join(home, ".cline"),
		Cursor:   filepath.Join(home, ".cursor"),
		ZCode:    filepath.Join(home, ".zcode", "cli", "db"),
		DeepSeek: filepath.Join(home, ".dsh"),
		Copilot:  filepath.Join(home, ".copilot"),
	}
	if got != want {
		t.Fatalf("roots =\n%+v\nwant\n%+v", got, want)
	}
}

func TestResolveRootsOverrides(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		env  map[string]string
		get  func(Roots) string
		want string
	}{
		{"CODEX_HOME", map[string]string{"CODEX_HOME": dir}, func(r Roots) string { return r.Codex }, dir},
		{"CLAUDE_CONFIG_DIR", map[string]string{"CLAUDE_CONFIG_DIR": dir}, func(r Roots) string { return r.Claude }, dir},
		{"PI_CODING_AGENT_DIR", map[string]string{"PI_CODING_AGENT_DIR": dir}, func(r Roots) string { return r.PiAgent }, dir},
		{"COPILOT_HOME", map[string]string{"COPILOT_HOME": dir}, func(r Roots) string { return r.Copilot }, dir},
		{"KIMI_CODE_HOME", map[string]string{"KIMI_CODE_HOME": dir}, func(r Roots) string { return r.Kimi }, dir},
		{"CLINE_DIR", map[string]string{"CLINE_DIR": dir}, func(r Roots) string { return r.Cline }, dir},
		{"ZCODE_HOME", map[string]string{"ZCODE_HOME": dir}, func(r Roots) string { return r.ZCode }, dir},
		{"DSH_HOME", map[string]string{"DSH_HOME": dir}, func(r Roots) string { return r.DeepSeek }, dir},
		// OpenCode hangs off the XDG data dir, not the variable's bare value.
		{"XDG_DATA_HOME", map[string]string{"XDG_DATA_HOME": dir}, func(r Roots) string { return r.OpenCode }, filepath.Join(dir, "opencode")},
		// Cursor prefers its own variable, then XDG, then home.
		{"CURSOR_CONFIG_DIR wins over XDG", map[string]string{"CURSOR_CONFIG_DIR": dir, "XDG_CONFIG_HOME": "/other"}, func(r Roots) string { return r.Cursor }, dir},
		{"XDG_CONFIG_HOME", map[string]string{"XDG_CONFIG_HOME": dir}, func(r Roots) string { return r.Cursor }, filepath.Join(dir, "cursor")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.get(ResolveRoots(envFrom(tc.env), t.TempDir())); got != tc.want {
				t.Fatalf("root = %q, want %q", got, tc.want)
			}
		})
	}
}

// Roots documents that every field is an absolute path. A relative root would
// be resolved against the process's working directory, so a provider would
// look for history somewhere inside the user's project.
func TestResolveRootsAreAbsolute(t *testing.T) {
	r := ResolveRoots(noEnv, t.TempDir())
	for name, path := range map[string]string{
		"Codex": r.Codex, "Claude": r.Claude, "Agy": r.Agy, "OpenCode": r.OpenCode,
		"PiAgent": r.PiAgent, "Kimi": r.Kimi, "Cline": r.Cline, "Cursor": r.Cursor,
		"ZCode": r.ZCode, "DeepSeek": r.DeepSeek,
	} {
		if path == "" {
			t.Errorf("%s root is empty", name)
		} else if !filepath.IsAbs(path) {
			t.Errorf("%s root = %q, want an absolute path", name, path)
		}
	}
}

// Skill directories carry the same absolute-path requirement as roots, and for
// a sharper reason: install-skill writes SKILL.md there, so a relative entry
// would stamp the file into whatever directory catchup was run from instead of
// the agent's global skills dir.
func TestResolveSkillDirsCoverEveryProvider(t *testing.T) {
	home := t.TempDir()
	dirs := ResolveSkillDirs(ResolveRoots(noEnv, home), home)
	for _, p := range allProviders {
		dir, ok := dirs[p]
		if !ok {
			t.Errorf("no skill dir for provider %q", p)
			continue
		}
		if !filepath.IsAbs(dir) {
			t.Errorf("skill dir for %q = %q, want an absolute path", p, dir)
		}
	}
	if len(dirs) != len(allProviders) {
		t.Errorf("skill dirs = %d entries, want %d: %+v", len(dirs), len(allProviders), dirs)
	}
}

// The skill dirs that follow a provider's history root must track an override
// of that root; the ones pinned to a fixed convention must not.
func TestResolveSkillDirsFollowOverrides(t *testing.T) {
	home, claude, dsh, data := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	env := map[string]string{
		"CLAUDE_CONFIG_DIR": claude,
		"DSH_HOME":          dsh,
		"XDG_DATA_HOME":     data,
	}
	dirs := ResolveSkillDirs(ResolveRoots(envFrom(env), home), home)
	for p, want := range map[string]string{
		ProviderClaude:   filepath.Join(claude, "skills"),
		ProviderDeepSeek: filepath.Join(dsh, "skills"),
		// OpenCode discovers skills under ~/.config, not $XDG_DATA_HOME, so
		// its history override must leave the skill dir alone — and the dir
		// must still be rooted at home.
		ProviderOpenCode: filepath.Join(home, ".config", "opencode", "skills"),
		// Codex's dir is fixed, and ZCode shares that same entry.
		ProviderCodex: filepath.Join(home, ".agents", "skills"),
		ProviderZCode: filepath.Join(home, ".agents", "skills"),
	} {
		if got := dirs[p]; got != want {
			t.Errorf("skill dir for %q = %q, want %q", p, got, want)
		}
	}
}

func TestResolveCurrent(t *testing.T) {
	if got := ResolveCurrent(noEnv); len(got) != 0 {
		t.Fatalf("current = %+v, want empty", got)
	}
	got := ResolveCurrent(envFrom(map[string]string{"CLAUDE_CODE_SESSION_ID": "sess-1"}))
	if len(got) != 1 || got[ProviderClaude] != "sess-1" {
		t.Fatalf("current = %+v", got)
	}
	got = ResolveCurrent(envFrom(map[string]string{"COPILOT_AGENT_SESSION_ID": "sess-2"}))
	if len(got) != 1 || got[ProviderCopilot] != "sess-2" {
		t.Fatalf("current = %+v", got)
	}
}
