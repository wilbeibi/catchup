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
//	Kimi     : $KIMI_CODE_HOME      else <home>/.kimi-code
//	Cline    : $CLINE_DIR           else <home>/.cline
//	Cursor   : $CURSOR_CONFIG_DIR   else $XDG_CONFIG_HOME/cursor else <home>/.cursor
//	ZCode    : $ZCODE_HOME          else <home>/.zcode/cli/db (the dir holding db.sqlite)
//	DeepSeek : $DSH_HOME            else <home>/.dsh
//	Copilot  : $COPILOT_HOME        else <home>/.copilot
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

	kimi := getenv("KIMI_CODE_HOME")
	if kimi == "" {
		kimi = filepath.Join(home, ".kimi-code")
	}

	cline := getenv("CLINE_DIR")
	if cline == "" {
		cline = filepath.Join(home, ".cline")
	}

	cursor := getenv("CURSOR_CONFIG_DIR")
	if cursor == "" {
		if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
			cursor = filepath.Join(xdg, "cursor")
		} else {
			cursor = filepath.Join(home, ".cursor")
		}
	}

	// The dir holding db.sqlite, not the file, so tests can point at a temp dir.
	zcode := getenv("ZCODE_HOME")
	if zcode == "" {
		zcode = filepath.Join(home, ".zcode", "cli", "db")
	}

	deepseek := getenv("DSH_HOME")
	if deepseek == "" {
		deepseek = filepath.Join(home, ".dsh")
	}

	copilot := getenv("COPILOT_HOME")
	if copilot == "" {
		copilot = filepath.Join(home, ".copilot")
	}

	return Roots{Codex: codex, Claude: claude, Agy: agy, OpenCode: opencode, PiAgent: piAgent, Kimi: kimi, Cline: cline, Cursor: cursor, ZCode: zcode, DeepSeek: deepseek, Copilot: copilot}
}

// ResolveSkillDirs returns each provider's global Agent Skills directory,
// keyed by provider name — the base path under which "catchup/SKILL.md" is
// installed. These follow each agent's own skill-discovery convention, which
// is not always the provider's history root:
//
//	Codex    : <home>/.agents/skills          (fixed; ignores $CODEX_HOME)
//	Claude   : roots.Claude/skills             (respects $CLAUDE_CONFIG_DIR)
//	Agy      : <home>/.gemini/config/skills    (the one dir all three
//	           Antigravity flavors — AGY, CLI, IDE — discover; paths like
//	           ~/.gemini/skills or ~/.gemini/antigravity-cli/skills are
//	           flavor-specific)
//	OpenCode : <home>/.config/opencode/skills  (fixed; not $XDG_DATA_HOME)
//	PiAgent  : roots.PiAgent/skills            (respects $PI_CODING_AGENT_DIR)
//	Kimi     : roots.Kimi/skills               (respects $KIMI_CODE_HOME; kimi
//	           also discovers ~/.agents/skills, but that path is Codex's entry
//	           — one dir per provider keeps installs and drift checks from
//	           stamping the same file twice)
//	Cline    : roots.Cline/skills            (respects $CLINE_DIR; cline also
//	           discovers ~/.agents/skills — Codex's entry, same reasoning)
//	Cursor   : roots.Cursor/skills           (respects $CURSOR_CONFIG_DIR)
//	ZCode    : <home>/.agents/skills         (shares Codex's entry: ZCode
//	           discovers ~/.agents/skills, so the same SKILL.md serves both)
//	Copilot  : roots.Copilot/skills          (respects $COPILOT_HOME; Copilot
//	           also discovers ~/.agents/skills — Codex's entry, same reasoning)
//	DeepSeek : roots.DeepSeek/skills         (respects $DSH_HOME; dsh also
//	           discovers ~/.agents/skills — Codex's entry, same reasoning)
func ResolveSkillDirs(roots Roots, home string) map[string]string {
	return map[string]string{
		ProviderCodex:    filepath.Join(home, ".agents", "skills"),
		ProviderClaude:   filepath.Join(roots.Claude, "skills"),
		ProviderAgy:      filepath.Join(home, ".gemini", "config", "skills"),
		ProviderOpenCode: filepath.Join(home, ".config", "opencode", "skills"),
		ProviderPiAgent:  filepath.Join(roots.PiAgent, "skills"),
		ProviderKimi:     filepath.Join(roots.Kimi, "skills"),
		ProviderCline:    filepath.Join(roots.Cline, "skills"),
		ProviderCursor:   filepath.Join(roots.Cursor, "skills"),
		ProviderZCode:    filepath.Join(home, ".agents", "skills"),
		ProviderDeepSeek: filepath.Join(roots.DeepSeek, "skills"),
		ProviderCopilot:  filepath.Join(roots.Copilot, "skills"),
	}
}

// ResolveCurrent reports the session each provider says we are running inside,
// keyed by provider name. Two agents inject such a signal into every shell
// they spawn: Claude Code ($CLAUDE_CODE_SESSION_ID) and Copilot CLI
// ($COPILOT_AGENT_SESSION_ID, whose value is the id --resume takes); Codex and
// OpenCode spawn shells indistinguishable from a plain terminal, so they
// contribute nothing. A provider absent from the map (or mapped to "") has no in-band
// current session, and the caller falls back to the newest session in the
// working directory.
//
// getenv is injected for the reason given on ResolveRoots.
func ResolveCurrent(getenv func(string) string) map[string]string {
	current := map[string]string{}
	if id := getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
		current[ProviderClaude] = id
	}
	if id := getenv("COPILOT_AGENT_SESSION_ID"); id != "" {
		current[ProviderCopilot] = id
	}
	return current
}
