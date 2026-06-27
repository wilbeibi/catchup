package render

import (
	"sort"
	"strings"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

// tsHuman is the compact, local-friendly timestamp used in headings and tables.
const tsHuman = "2006-01-02 15:04"

// kv is an ordered key/value pair for descriptive output (frontmatter, tables).
type kv struct{ Key, Val string }

// metaOrder is the preferred display order for known metadata keys. Providers
// normalize onto these names; anything else is shown afterward, alphabetically.
var metaOrder = []string{"title", "cwd", "branch", "model", "model_provider", "cli_version", "agent", "parent"}

// header projects a Source into ordered display pairs: structural fields first,
// then known metadata in metaOrder, then any remaining metadata sorted. This is
// the single definition of "how a session describes itself" shared by the
// Markdown frontmatter and the HTML header.
func header(src session.Source) []kv {
	pairs := []kv{{"provider", src.Ref.Provider}}
	if src.Ref.SessionID != "" {
		pairs = append(pairs, kv{"session", src.Ref.SessionID})
	}
	if !src.UpdatedAt.IsZero() {
		pairs = append(pairs, kv{"updated", src.UpdatedAt.UTC().Format(time.RFC3339)})
	}
	if src.Path != "" {
		pairs = append(pairs, kv{"path", src.Path})
	}

	seen := make(map[string]bool, len(metaOrder))
	for _, k := range metaOrder {
		if v := src.Metadata[k]; v != "" {
			pairs = append(pairs, kv{k, v})
			seen[k] = true
		}
	}
	rest := make([]string, 0, len(src.Metadata))
	for k, v := range src.Metadata {
		if v != "" && !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		pairs = append(pairs, kv{k, src.Metadata[k]})
	}
	return pairs
}

// entryLabel is the short role/kind tag shown for a timeline entry.
func entryLabel(e session.Entry) string {
	switch {
	case e.Kind == session.KindCompact:
		return "compact"
	case e.Role != "":
		return e.Role
	default:
		return "message"
	}
}

// oneLine collapses all runs of whitespace (including newlines) into single
// spaces, for previews and table cells.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncate shortens s to at most n runes, appending an ellipsis when it cuts.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
