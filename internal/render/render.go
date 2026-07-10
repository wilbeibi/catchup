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
// before it gets here.
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

// tableList renders the human listing. It adapts to terminal width: columns
// are "#", "UPDATED", "TITLE", "SESSION". TITLE gets the remaining space
// after fixed columns and the longest session ID in the batch. Columns are
// aligned with display-width-aware padding so CJK characters (2 columns each
// in terminals) align correctly.
func tableList(w io.Writer, provider string, summaries []session.Summary) error {
	if len(summaries) == 0 {
		_, err := fmt.Fprintf(w, "no %s sessions found\n", provider)
		return err
	}

	const gutter = 1
	rankW := 3 // fits ranks up to 999
	updW := 16 // "2006-01-02 15:04"
	sidW := maxSidWidth(summaries)
	titleW := termWidth(w) - rankW - updW - sidW - 3*gutter
	if titleW < 15 {
		titleW = 15
	}
	if titleW > 80 {
		titleW = 80
	}

	// Header
	fmt.Fprintf(w, "%s %s %s %s\n",
		runewidth.FillRight("#", rankW),
		runewidth.FillRight("UPDATED", updW),
		runewidth.FillRight("TITLE", titleW),
		runewidth.FillRight("SESSION", sidW),
	)

	for _, s := range summaries {
		updated := ""
		if !s.UpdatedAt.IsZero() {
			updated = s.UpdatedAt.Local().Format(tsHuman)
		}
		title := runewidth.Truncate(oneLine(s.Title), titleW, "…")
		fmt.Fprintf(w, "%s %s %s %s\n",
			runewidth.FillRight(fmt.Sprintf("%d", s.Rank), rankW),
			runewidth.FillRight(updated, updW),
			runewidth.FillRight(title, titleW),
			s.Ref.SessionID,
		)
	}
	return nil
}

// maxSidWidth returns the maximum display width of session IDs in the batch,
// clamped to at least the width of the header "SESSION" (7).
func maxSidWidth(summaries []session.Summary) int {
	m := 7 // len("SESSION")
	for _, s := range summaries {
		if w := runewidth.StringWidth(s.Ref.SessionID); w > m {
			m = w
		}
	}
	return m
}
