package render

import (
	"fmt"
	"io"

	"github.com/wilbeibi/baton/internal/session"
)

// htmlThread renders a self-contained HTML document with inline CSS and no
// JavaScript. It will use html/template so that all interpolated text is
// escaped by construction rather than by hand.
func htmlThread(w io.Writer, t session.Thread) error {
	return fmt.Errorf("render: htmlThread not implemented")
}

// htmlMeta renders a Source's metadata as a standalone HTML fragment.
func htmlMeta(w io.Writer, s session.Source) error {
	return fmt.Errorf("render: htmlMeta not implemented")
}
