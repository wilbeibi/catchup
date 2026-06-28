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
	"text/tabwriter"

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

// Meta renders only a session's metadata/frontmatter (the -I view).
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

// List renders a ranked listing as a plain table. It adapts to terminal width:
// columns are "#", "UPDATED", "TITLE", "SESSION". TITLE gets the remaining
// space after fixed columns and the longest session ID in the batch.
func List(w io.Writer, provider string, summaries []session.Summary) error {
	if len(summaries) == 0 {
		_, err := fmt.Fprintf(w, "no %s sessions found\n", provider)
		return err
	}

	titleMax := listTitleWidth(w, summaries)
	if titleMax < 15 {
		titleMax = 15
	}
	if titleMax > 80 {
		titleMax = 80
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tUPDATED\tTITLE\tSESSION")
	for _, s := range summaries {
		updated := ""
		if !s.UpdatedAt.IsZero() {
			updated = s.UpdatedAt.Local().Format(tsHuman)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n",
			s.Rank,
			updated,
			truncate(oneLine(s.Title), titleMax),
			s.Ref.SessionID,
		)
	}
	return tw.Flush()
}

// listTitleWidth computes how many runes the TITLE column gets after
// accounting for the other fixed columns and tabwriter padding (2 per cell).
// It scans summaries for the longest session ID to use as the SESSION column
// width, so the full ID always fits.
func listTitleWidth(w io.Writer, summaries []session.Summary) int {
	maxSid := len("SESSION") // at least the header width
	for _, s := range summaries {
		if len(s.Ref.SessionID) > maxSid {
			maxSid = len(s.Ref.SessionID)
		}
	}
	// Fixed: "#" (max 3 for ranks up to 999) + "UPDATED" (16) + SESSION (maxSid) + 8 padding
	fixed := 3 + 16 + maxSid + 8
	return termWidth(w) - fixed
}
