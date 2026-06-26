package render

import (
	"fmt"
	"io"

	"github.com/wilbeibi/baton/internal/session"
)

// markdownThread writes YAML frontmatter followed by a numbered timeline, built
// with a strings.Builder. No template engine: Markdown is line-oriented and a
// builder keeps the output fully under our control (stable key order, exact
// blank lines).
func markdownThread(w io.Writer, t session.Thread) error {
	return fmt.Errorf("render: markdownThread not implemented")
}

// markdownMeta writes just the YAML frontmatter block for a Source.
func markdownMeta(w io.Writer, s session.Source) error {
	return fmt.Errorf("render: markdownMeta not implemented")
}
