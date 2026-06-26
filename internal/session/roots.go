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
