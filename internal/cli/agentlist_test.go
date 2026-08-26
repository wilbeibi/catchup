package cli

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/wilbeibi/catchup/internal/session"
)

// Five hand-written lists name the agents catchup reads: the help text, both
// agent lists in README.md, and both in SKILL.md. Nothing in the build ties
// them to the providers that exist, so a new provider can ship with any of
// them stale. This has not happened yet — it is a silent failure mode, not an
// observed bug, and the skill's frontmatter is why it is worth guarding: an
// agent reading a description that omits the agent its user just named
// concludes catchup cannot read that agent, and the skill never fires. No
// error appears anywhere. The other four merely mislead a reader.
//
// The sixth list, the unknown-agent error, is not here because it is built
// from session.Providers and cannot drift.

// supportedAgents returns the provider set, having first checked that the two
// registries agree with it: every name must resolve to an implementation, and
// must have a skill directory, or install-skill has nowhere to write.
func supportedAgents(t *testing.T) []string {
	t.Helper()
	skillDirs := session.ResolveSkillDirs(session.Roots{}, "/home/u")
	names := append([]string(nil), session.Providers...)
	for _, name := range names {
		if _, err := selectProvider(name); err != nil {
			t.Errorf("session.Providers lists %q but selectProvider rejects it: %v", name, err)
		}
		if _, ok := skillDirs[name]; !ok {
			t.Errorf("%q has no skill directory; install-skill would have nowhere to write it", name)
		}
	}
	if len(skillDirs) != len(names) {
		t.Errorf("ResolveSkillDirs covers %d agents, session.Providers lists %d", len(skillDirs), len(names))
	}
	sort.Strings(names)
	return names
}

// parseAgentList reads an id list back into agent names, dropping the
// decoration the surfaces carry: backticks, a parenthetical display name after
// the id, and the "or" before the last entry. README separates with "·" where
// the others use ",".
func parseAgentList(s string) []string {
	var names []string
	for _, field := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '·' }) {
		name, _, _ := strings.Cut(strings.TrimSpace(field), " (")
		name = strings.TrimPrefix(strings.Trim(name, "`. "), "or ")
		if name != "" {
			names = append(names, strings.Trim(name, "`"))
		}
	}
	sort.Strings(names)
	return names
}

// docLine returns the line of text starting with prefix, joining continuation
// lines while the text so far ends on a comma (the help text wraps its list).
// The prose checks are substring tests, so they have to run against one line:
// an agent dropped from README's opening sentence still appears further down in
// the id list, and a whole-file search would never notice.
func docLine(t *testing.T, text, prefix, what string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], prefix) {
			continue
		}
		line := strings.TrimPrefix(lines[i], prefix)
		for strings.HasSuffix(strings.TrimSpace(line), ",") && i+1 < len(lines) {
			i++
			line += " " + lines[i]
		}
		return line
	}
	t.Fatalf("%s has no line starting with %q", what, prefix)
	return ""
}

func readDoc(t *testing.T, name string) string {
	t.Helper()
	md, err := os.ReadFile("../../" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(md)
}

func TestDocumentedAgentListsMatchTheProviders(t *testing.T) {
	want := supportedAgents(t)

	for _, tc := range []struct {
		surface string
		list    string
	}{
		{"catchup --help", docLine(t, helpText, "Agents:", "help text")},
		{"SKILL.md", docLine(t, readDoc(t, "SKILL.md"), "Agents:", "SKILL.md")},
		{"README.md", docLine(t, readDoc(t, "README.md"), "Agents:", "README.md")},
	} {
		t.Run(tc.surface, func(t *testing.T) {
			got := parseAgentList(tc.list)
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("%s lists\n  %v\nbut catchup supports\n  %v", tc.surface, got, want)
			}
		})
	}
}

// Two surfaces name the agents the way a user would rather than by id: the
// skill's frontmatter description and README's opening line. That mapping is a
// genuine second copy of the agent list, restated here rather than derived
// because only a human can say what a new provider is called in a sentence.
// Adding a provider fails this test until the name is chosen.
func TestProseNamesEveryAgent(t *testing.T) {
	displayNames := map[string]string{
		session.ProviderCodex:    "Codex",
		session.ProviderClaude:   "Claude Code",
		session.ProviderAgy:      "Antigravity",
		session.ProviderCline:    "Cline",
		session.ProviderCopilot:  "Copilot CLI",
		session.ProviderCursor:   "Cursor",
		session.ProviderDeepSeek: "DeepSeek Harness",
		session.ProviderKimi:     "Kimi",
		session.ProviderOpenCode: "OpenCode",
		session.ProviderPiAgent:  "Pi Agent",
		session.ProviderZCode:    "ZCode",
	}

	agents := supportedAgents(t)
	if len(displayNames) != len(agents) {
		t.Errorf("display names cover %d agents, catchup supports %d", len(displayNames), len(agents))
	}

	description, _, _ := strings.Cut(readDoc(t, "SKILL.md"), "\n---\n\n")
	surfaces := map[string]string{
		"SKILL.md description": description,
		"README.md intro":      docLine(t, readDoc(t, "README.md"), "Works with", "README.md"),
	}

	for surface, text := range surfaces {
		t.Run(surface, func(t *testing.T) {
			for _, name := range agents {
				display, ok := displayNames[name]
				if !ok {
					t.Errorf("%s has no display name; add one and name it in the prose", name)
					continue
				}
				if !strings.Contains(text, display) {
					t.Errorf("%s never says %q, so a reader looking for %s will not find it", surface, display, name)
				}
			}
		})
	}
}
