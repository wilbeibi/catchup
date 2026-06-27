package session

import "path/filepath"

// ResolveRoots determines each provider's history location from the
// environment, falling back to the conventional default under home when the
// override variable is unset.
//
//	Codex    : $CODEX_HOME          else <home>/.codex
//	Claude   : $CLAUDE_CONFIG_DIR   else <home>/.claude
//	OpenCode : $XDG_DATA_HOME/opencode else <home>/.local/share/opencode
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

	opencode := getenv("XDG_DATA_HOME")
	if opencode != "" {
		opencode = filepath.Join(opencode, "opencode")
	} else {
		opencode = filepath.Join(home, ".local", "share", "opencode")
	}

	return Roots{Codex: codex, Claude: claude, OpenCode: opencode}
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
