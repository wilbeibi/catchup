// Package render turns the core data types into bytes. Every function is a pure
// transformation from a session value to a Writer: it performs no history lookup
// and makes no decisions about which session to show. There are three encodings
// (Markdown, HTML, JSON) and three views (a full Thread, a Source's metadata, a
// listing). The Format is a closed set, so dispatch is a switch rather than an
// interface — polymorphism lives at the Provider boundary, not here.
package render

import (
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	"github.com/mattn/go-runewidth"
	"github.com/wilbeibi/catchup/internal/session"
)

// Thread renders a full conversation timeline in the requested format.
func Thread(w io.Writer, t session.Thread, f session.Format) error {
	switch f {
	case session.FormatMarkdown:
		return markdownThread(w, t)
	case session.FormatHTML:
		return htmlThread(w, t)
	case session.FormatJSON:
		return jsonThread(w, t)
	default:
		return fmt.Errorf("render: unsupported format %s", f)
	}
}

// Meta renders only a session's metadata/frontmatter (the -i view).
func Meta(w io.Writer, s session.Source, f session.Format) error {
	switch f {
	case session.FormatMarkdown:
		return markdownMeta(w, s)
	case session.FormatHTML:
		return htmlMeta(w, s)
	case session.FormatJSON:
		return jsonMeta(w, s)
	default:
		return fmt.Errorf("render: unsupported format %s", f)
	}
}

// List renders a ranked listing: a plain table by default, or a JSON array
// for scripts. HTML has no listing view; the cli rejects that combination
// before it gets here. provider names the agent every row belongs to, and is
// empty for a cross-agent listing, where each row carries its own.
func List(w io.Writer, provider string, summaries []session.Summary, f session.Format) error {
	switch f {
	case session.FormatJSON:
		return jsonList(w, summaries)
	case session.FormatMarkdown:
		return tableList(w, provider, summaries)
	default:
		return fmt.Errorf("render: unsupported listing format %s", f)
	}
}

// tableList renders the human listing: columns are "SESSION", "UPDATED",
// "TITLE". SESSION is the selector the reader would retype (claude/3), not the
// session id — nobody types a UUID by hand, and the id is one --json away for
// the scripts that want it. TITLE takes whatever width is left, since it is the
// only column that answers "was this the one?". Columns are aligned with
// display-width-aware padding so CJK characters (2 columns each in terminals)
// align correctly.
func tableList(w io.Writer, provider string, summaries []session.Summary) error {
	if len(summaries) == 0 {
		if provider == "" {
			_, err := fmt.Fprintln(w, "no sessions found")
			return err
		}
		_, err := fmt.Fprintf(w, "no %s sessions found\n", provider)
		return err
	}

	const gutter = 1
	handles := make([]string, len(summaries))
	for i, s := range summaries {
		handles[i] = handle(s, provider)
	}
	ages := make([]string, len(summaries))
	for i, s := range summaries {
		ages[i] = Age(s.UpdatedAt)
	}
	selW := maxWidth("SESSION", handles)
	updW := maxWidth("UPDATED", ages)
	titleW := termWidth(w) - selW - updW - 2*gutter
	if titleW < 15 {
		titleW = 15
	}
	if titleW > 80 {
		titleW = 80
	}

	// Header
	fmt.Fprintf(w, "%s %s %s\n",
		runewidth.FillRight("SESSION", selW),
		runewidth.FillRight("UPDATED", updW),
		"TITLE",
	)

	for i, s := range summaries {
		fmt.Fprintf(w, "%s %s %s\n",
			runewidth.FillRight(handles[i], selW),
			runewidth.FillRight(ages[i], updW),
			runewidth.Truncate(titleCell(s), titleW, "…"),
		)
	}
	return nil
}

// titleCell is the text that has to answer "was this the one?". Providers whose
// agent never named the session fall back to the directory name, which says
// nothing in a listing already scoped to one directory — every row reads the
// same. The session's opening message is what actually tells those rows apart,
// so it stands in.
func titleCell(s session.Summary) string {
	title := oneLine(s.Title)
	if s.Preview == "" {
		return title
	}
	if title == "" || title == filepath.Base(s.Cwd) {
		return oneLine(s.Preview)
	}
	return title
}

// handle is the "<agent>/<rank>" selector that re-selects a listed row on a
// later invocation. A row's own provider wins over the listing's, so a
// cross-agent table labels every row correctly; fallback covers the providers
// whose List leaves Ref.Provider unstamped.
func handle(s session.Summary, provider string) string {
	name := s.Ref.Provider
	if name == "" {
		name = provider
	}
	if name == "" {
		return strconv.Itoa(s.Rank)
	}
	return name + "/" + strconv.Itoa(s.Rank)
}

// maxWidth returns the display width of the widest of a header and its cells.
func maxWidth(header string, cells []string) int {
	m := runewidth.StringWidth(header)
	for _, c := range cells {
		if w := runewidth.StringWidth(c); w > m {
			m = w
		}
	}
	return m
}
