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

	"github.com/wilbeibi/baton/internal/session"
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

// List renders a ranked listing as a plain table. Listings are always textual
// (rank, updated time, title/cwd, preview, session id), independent of the
// Thread output format, so they do not take a Format.
func List(w io.Writer, provider string, summaries []session.Summary) error {
	if len(summaries) == 0 {
		_, err := fmt.Fprintf(w, "no %s sessions found\n", provider)
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RANK\tUPDATED\tTITLE\tCWD\tPREVIEW\tSESSION")
	for _, s := range summaries {
		updated := ""
		if !s.UpdatedAt.IsZero() {
			updated = s.UpdatedAt.Local().Format(tsHuman)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n",
			s.Rank,
			updated,
			truncate(oneLine(s.Title), 40),
			truncate(oneLine(s.Cwd), 32),
			truncate(oneLine(s.Preview), 50),
			s.Ref.SessionID,
		)
	}
	return tw.Flush()
}
