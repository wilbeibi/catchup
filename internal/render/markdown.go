package render

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/wilbeibi/baton/internal/session"
)

// markdownThread writes YAML frontmatter followed by a numbered timeline, built
// with a strings.Builder. No template engine: Markdown is line-oriented and a
// builder keeps the output fully under our control (stable key order, exact
// blank lines).
func markdownThread(w io.Writer, t session.Thread) error {
	var b strings.Builder
	extra := []kv{{"entries", strconv.Itoa(len(t.Entries))}}
	writeFrontmatter(&b, t.Source, allWarnings(t), extra)

	b.WriteByte('\n')
	for i, e := range t.Entries {
		writeEntry(&b, i+1, e)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// markdownMeta writes just the YAML frontmatter block for a Source.
func markdownMeta(w io.Writer, s session.Source) error {
	var b strings.Builder
	writeFrontmatter(&b, s, s.Warnings, nil)
	_, err := io.WriteString(w, b.String())
	return err
}

func writeFrontmatter(b *strings.Builder, src session.Source, warnings []string, extra []kv) {
	b.WriteString("---\n")
	for _, p := range header(src) {
		writeYAMLField(b, p.Key, p.Val)
	}
	for _, p := range extra {
		writeYAMLField(b, p.Key, p.Val)
	}
	if len(warnings) > 0 {
		b.WriteString("warnings:\n")
		for _, wmsg := range warnings {
			b.WriteString("  - ")
			b.WriteString(yamlScalar(wmsg))
			b.WriteByte('\n')
		}
	}
	b.WriteString("---\n")
}

func writeEntry(b *strings.Builder, n int, e session.Entry) {
	fmt.Fprintf(b, "## %d. %s", n, entryLabel(e))
	if !e.Time.IsZero() {
		b.WriteString(" · ")
		b.WriteString(e.Time.UTC().Format(tsHuman))
	}
	b.WriteString("\n\n")

	text := strings.TrimRight(e.Text, "\n")
	if text == "" && e.Kind == session.KindCompact {
		text = "_(context compacted)_"
	}
	b.WriteString(text)
	b.WriteString("\n\n")
}

func writeYAMLField(b *strings.Builder, key, val string) {
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(yamlScalar(val))
	b.WriteByte('\n')
}

// yamlScalar renders a string as a YAML scalar, quoting only when the plain form
// would be ambiguous. Frontmatter values here are single-line (titles, paths,
// ids), so a double-quoted form with minimal escaping is sufficient.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if yamlNeedsQuote(s) {
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
		return `"` + r.Replace(s) + `"`
	}
	return s
}

func yamlNeedsQuote(s string) bool {
	if s != strings.TrimSpace(s) {
		return true
	}
	if strings.ContainsAny(s, ":#{}[],&*!|>'\"%@`\n\t") {
		return true
	}
	switch s[0] {
	case '-', '?', ' ':
		return true
	}
	return false
}

// allWarnings merges a thread's own warnings with its source's, dropping
// duplicates while preserving order.
func allWarnings(t session.Thread) []string {
	if len(t.Source.Warnings) == 0 {
		return t.Warnings
	}
	seen := make(map[string]bool)
	merged := append(append([]string{}, t.Source.Warnings...), t.Warnings...)
	out := make([]string, 0, len(merged))
	for _, w := range merged {
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}
