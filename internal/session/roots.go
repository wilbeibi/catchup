package session

import "path/filepath"

// ResolveRoots determines each provider's history location from the
// environment, falling back to the conventional default under home when the
// override variable is unset.
//
//	Codex    : $CODEX_HOME          else <home>/.codex
//	Claude   : $CLAUDE_CONFIG_DIR   else <home>/.claude
//	Agy      : <home>/.gemini/antigravity-cli (Antigravity documents no override)
//	OpenCode : $XDG_DATA_HOME/opencode else <home>/.local/share/opencode
//	PiAgent  : $PI_CODING_AGENT_DIR else <home>/.pi/agent
//
// getenv and home are passed in rather than read from the os package so that
// root resolution is a pure function and can be tested without touching the
// real environment. main wires in os.Getenv and os.UserHomeDir.
func ResolveRoots(getenv func(string) string, home string) Roots {
	codex := getenv("CODEX_HOME")
	if codex == "" {
		codex = filepath.Join(home, ".codex")
	}

	claude := getenv("CLAUDE_CONFIG_DIR")
	if claude == "" {
		claude = filepath.Join(home, ".claude")
	}

	agy := filepath.Join(home, ".gemini", "antigravity-cli")

	opencode := getenv("XDG_DATA_HOME")
	if opencode != "" {
		opencode = filepath.Join(opencode, "opencode")
	} else {
		opencode = filepath.Join(home, ".local", "share", "opencode")
	}

	piAgent := getenv("PI_CODING_AGENT_DIR")
	if piAgent == "" {
		piAgent = filepath.Join(home, ".pi", "agent")
	}

	return Roots{Codex: codex, Claude: claude, Agy: agy, OpenCode: opencode, PiAgent: piAgent}
}

// ResolveSkillDirs returns each provider's global Agent Skills directory,
// keyed by provider name — the base path under which "catchup/SKILL.md" is
// installed. These follow each agent's own skill-discovery convention, which
// is not always the provider's history root:
//
//	Codex    : <home>/.agents/skills          (fixed; ignores $CODEX_HOME)
//	Claude   : roots.Claude/skills             (respects $CLAUDE_CONFIG_DIR)
//	OpenCode : <home>/.config/opencode/skills  (fixed; not $XDG_DATA_HOME)
//	PiAgent  : roots.PiAgent/skills            (respects $PI_CODING_AGENT_DIR)
//
// Agy is deliberately absent: Antigravity ships builtin skills but documents
// no user skills directory, and installSkill skips providers without an
// entry rather than writing to a guessed path.
func ResolveSkillDirs(roots Roots, home string) map[string]string {
	return map[string]string{
		ProviderCodex:    filepath.Join(home, ".agents", "skills"),
		ProviderClaude:   filepath.Join(roots.Claude, "skills"),
		ProviderOpenCode: filepath.Join(home, ".config", "opencode", "skills"),
		ProviderPiAgent:  filepath.Join(roots.PiAgent, "skills"),
	}
}

// ResolveCurrent reports the session each provider says we are running inside,
// keyed by provider name. Only Claude Code injects such a signal today
// ($CLAUDE_CODE_SESSION_ID, set in every shell it spawns); Codex and OpenCode
// spawn shells indistinguishable from a plain terminal, so they contribute
// nothing. A provider absent from the map (or mapped to "") has no in-band
// current session, and the caller falls back to the newest session in the
// working directory.
//
// Like ResolveRoots, getenv is passed in rather than read from os so resolution
// stays a pure, testable function; main wires in os.Getenv.
func ResolveCurrent(getenv func(string) string) map[string]string {
	current := map[string]string{}
	if id := getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
		current[ProviderClaude] = id
	}
	return current
}
