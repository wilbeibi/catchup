package session

import (
	"unicode"
	"unicode/utf8"
)

// Match is the passage that satisfied a listing's keyword query: the reason a
// row is in the result. A listing scoped by -q otherwise answers "which
// sessions" without answering "why", and the title it shows instead is often
// the agent's name for the whole session or, failing that, the directory every
// row shares.
//
// Text is a literal span of the session, never a summary — catchup's reader can
// summarize and this cannot, so the excerpt only ever quotes. It keeps its
// original newlines; a renderer that needs one line collapses them itself, and
// the ellipses that mark the two Truncated flags are likewise the renderer's to
// draw, so the span stays exactly what the file holds.
type Match struct {
	Role string
	Kind string
	Text string

	// TruncatedBefore and TruncatedAfter report that Text starts or stops
	// mid-entry, so a reader knows the passage is a window and not the whole
	// message.
	TruncatedBefore bool
	TruncatedAfter  bool
}

// An excerpt is capped in runes rather than bytes so a CJK passage carries as
// many characters as an ASCII one. The budget is wider than any terminal row
// because the table truncates to its own width, while --json hands the whole
// span to an agent that wants the context around the hit.
//
// The lead is small on purpose. The table cuts the row at the terminal's width -
// 80 columns at its widest - so lead spent ahead of the match is width the match
// itself may not survive: a MATCH column that scrolls the searched-for word off
// the right edge shows context for something the reader cannot see.
const (
	excerptBudget = 240 // runes of an entry an excerpt may carry
	excerptLead   = 16  // how many of them are spent before the match
)

// Excerpt returns the passage of e that satisfies the query, or nil if e does
// not contain it. Providers that locate their match without building a Thread —
// the SQLite-backed ones ask their database — call this with an Entry of their
// own to get the same window every other provider produces.
func (o ListOptions) Excerpt(e Entry) *Match {
	if o.Query == "" {
		return nil
	}
	start, end := indexFold(e.Text, o.Query)
	if start < 0 {
		return nil
	}
	from, to := window(e.Text, start, end)
	return &Match{
		Role:            e.Role,
		Kind:            e.Kind,
		Text:            e.Text[from:to],
		TruncatedBefore: from > 0,
		TruncatedAfter:  to < len(e.Text),
	}
}

// Summarize projects t into a listing row and attaches the passage that matched.
// It is the listing counterpart of Thread.Summary, which cannot find the passage
// itself because it does not know the query.
func (o ListOptions) Summarize(t Thread) Summary {
	s := t.Summary()
	s.Match = o.firstMatch(t)
	return s
}

// firstMatch returns the earliest entry's excerpt, so a row quotes the first
// place the session says the thing rather than the last.
func (o ListOptions) firstMatch(t Thread) *Match {
	for _, e := range t.Entries {
		if m := o.Excerpt(e); m != nil {
			return m
		}
	}
	return nil
}

// window widens the matched span to at most excerptBudget runes of the entry
// around it, spending excerptLead of them ahead of the match so the reader sees
// what led into it.
//
// It anchors on the match even when the whole entry would fit, because the two
// ends clamp: a match within the first excerptLead runes of a short entry yields
// the entry whole, and a later one yields a window that the reader can see the
// match in. Quoting a short entry from its start instead would put the match
// past the right edge of a terminal, which is the one thing this column exists
// to show.
//
// The budget bounds the context, never the match: a query longer than the budget
// still comes back whole, because an excerpt that stops before the searched-for
// text answers nothing.
func window(s string, start, end int) (int, int) {
	from := backUp(s, start, excerptLead)
	return from, max(advance(s, from, excerptBudget), end)
}

// backUp moves off by n runes toward the start of s.
func backUp(s string, off, n int) int {
	for ; n > 0 && off > 0; n-- {
		_, size := utf8.DecodeLastRuneInString(s[:off])
		off -= size
	}
	return off
}

// advance moves off by n runes toward the end of s.
func advance(s string, off, n int) int {
	for ; n > 0 && off < len(s); n-- {
		_, size := utf8.DecodeRuneInString(s[off:])
		off += size
	}
	return off
}

// indexFold returns the byte range of the first case-insensitive occurrence of
// needle in s, or -1 when there is none.
//
// It walks s a rune at a time and folds as it compares, rather than searching a
// lowercased copy, because the returned range indexes s itself: unicode.ToLower
// can change a rune's width — KELVIN SIGN is three bytes and lowercases to a
// one-byte k — so an offset taken from the copy would slice the original in the
// wrong place, or between the bytes of a rune. Folding matches strings.ToLower
// rune for rune, so this decides exactly what MatchesQuery decided before it
// also had to say where.
func indexFold(s, needle string) (int, int) {
	for i := range s {
		if n, ok := matchFoldAt(s[i:], needle); ok {
			return i, i + n
		}
	}
	return -1, -1
}

// matchFoldAt reports whether s begins with needle, case-blind, and how many
// bytes of s that took — which is not len(needle) when the two spell the same
// characters at different widths.
func matchFoldAt(s, needle string) (int, bool) {
	i := 0
	for _, nr := range needle {
		if i >= len(s) {
			return 0, false
		}
		sr, size := utf8.DecodeRuneInString(s[i:])
		if unicode.ToLower(sr) != unicode.ToLower(nr) {
			return 0, false
		}
		i += size
	}
	return i, true
}
