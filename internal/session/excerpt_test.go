package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func query(q string) ListOptions { return ListOptions{Query: q} }

// A short message whose match is near its start is quoted whole, which is the
// common case and the one that must carry no ellipses.
func TestExcerptKeepsShortEntryWhole(t *testing.T) {
	e := Entry{Kind: KindMessage, Role: RoleUser, Text: "let's fix the egress rule"}
	m := query("EGRESS").Excerpt(e)
	if m == nil {
		t.Fatal("Excerpt = nil, want the entry")
	}
	if m.Text != e.Text {
		t.Errorf("Text = %q, want the whole entry %q", m.Text, e.Text)
	}
	if m.TruncatedBefore || m.TruncatedAfter {
		t.Error("a whole entry was reported as truncated")
	}
	if m.Role != RoleUser || m.Kind != KindMessage {
		t.Errorf("role/kind = %q/%q, want %q/%q", m.Role, m.Kind, RoleUser, KindMessage)
	}
}

// A long message is windowed around the hit, and the budget counts runes so a
// CJK passage carries as many characters as an ASCII one rather than a third.
func TestExcerptWindowsLongEntry(t *testing.T) {
	for _, filler := range []string{"a", "文"} {
		pad := strings.Repeat(filler, 500)
		e := Entry{Text: pad + "egress" + pad}
		m := query("egress").Excerpt(e)
		if m == nil {
			t.Fatalf("filler %q: Excerpt = nil", filler)
		}
		if !strings.Contains(m.Text, "egress") {
			t.Errorf("filler %q: excerpt does not contain the match: %q", filler, m.Text)
		}
		if n := utf8.RuneCountInString(m.Text); n != excerptBudget {
			t.Errorf("filler %q: excerpt is %d runes, want the %d-rune budget", filler, n, excerptBudget)
		}
		if !m.TruncatedBefore || !m.TruncatedAfter {
			t.Errorf("filler %q: a window into a longer message reported no truncation", filler)
		}
		// The lead is context, not padding: the match must not sit at the edge.
		if strings.HasPrefix(m.Text, "egress") {
			t.Errorf("filler %q: excerpt starts at the match, so it carries no lead context", filler)
		}
	}
}

// A hit near the start has no room for a full lead and simply begins at the
// message, which is not truncation and must not be marked as such.
func TestExcerptNearStartIsNotTruncatedBefore(t *testing.T) {
	e := Entry{Text: "egress " + strings.Repeat("a", 500)}
	m := query("egress").Excerpt(e)
	if m == nil {
		t.Fatal("Excerpt = nil")
	}
	if m.TruncatedBefore {
		t.Error("an excerpt starting at byte 0 was marked truncated before")
	}
	if !m.TruncatedAfter {
		t.Error("an excerpt stopping mid-message was not marked truncated after")
	}
}

// The budget bounds the context, not the match: a query wider than the whole
// excerpt still comes back whole, or the row would quote something that does not
// contain what was searched for.
func TestExcerptKeepsAMatchWiderThanTheBudget(t *testing.T) {
	q := strings.Repeat("egress ", excerptBudget/3)
	e := Entry{Text: strings.Repeat("a", 500) + q + strings.Repeat("a", 500)}
	m := query(q).Excerpt(e)
	if m == nil {
		t.Fatal("Excerpt = nil")
	}
	if !strings.Contains(m.Text, q) {
		t.Errorf("excerpt is %d runes and does not contain the %d-rune query",
			utf8.RuneCountInString(m.Text), utf8.RuneCountInString(q))
	}
}

// A match late in a short entry is still windowed, so the terminal does not cut
// the row off before it.
func TestExcerptWindowsALateMatchInAShortEntry(t *testing.T) {
	e := Entry{Text: strings.Repeat("a", 100) + " egress"}
	m := query("egress").Excerpt(e)
	if m == nil {
		t.Fatal("Excerpt = nil")
	}
	if !m.TruncatedBefore {
		t.Errorf("excerpt quoted the entry from its start: %q", m.Text)
	}
	if m.TruncatedAfter {
		t.Errorf("excerpt stopped short of the entry's end: %q", m.Text)
	}
	if n := utf8.RuneCountInString(m.Text); n > excerptLead+len("egress")+1 {
		t.Errorf("excerpt is %d runes, want a window of about %d", n, excerptLead)
	}
}

func TestExcerptWithoutAMatch(t *testing.T) {
	e := Entry{Text: "nothing relevant here"}
	if m := query("egress").Excerpt(e); m != nil {
		t.Errorf("Excerpt on a non-matching entry = %+v, want nil", m)
	}
	if m := query("").Excerpt(e); m != nil {
		t.Errorf("Excerpt with no query = %+v, want nil", m)
	}
}

// indexFold returns a range into the original string, which is the whole reason
// it exists rather than a strings.Index over a lowercased copy: KELVIN SIGN is
// three bytes and lowercases to a one-byte k, so an offset taken from the copy
// lands early for everything after it - inside a rune, in the worst case.
//
// The rune is written \u212A rather than pasted: a literal is easy to lose to a
// tool that rewrites this file, and the fixture would then quietly test ASCII.
func TestIndexFoldSpansOriginalBytes(t *testing.T) {
	cases := []struct{ s, needle, want string }{
		// The needle spells the wide rune, so a naive end offset - start plus the
		// needle's own length - stops two bytes short, inside the rune.
		{"a \u212Aelvin reading", "kelvin", "\u212Aelvin"},
		// The needle merely follows one, so a naive start offset lands two bytes
		// late: everything after the rune shifted when the copy was lowercased.
		{"\u212A then the egress fix", "egress", "egress"},
	}
	for _, c := range cases {
		start, end := indexFold(c.s, c.needle)
		if start < 0 {
			t.Errorf("indexFold(%q, %q) found nothing", c.s, c.needle)
			continue
		}
		if got := c.s[start:end]; got != c.want {
			t.Errorf("indexFold(%q, %q) spans %q, want %q: the range must index the original string",
				c.s, c.needle, got, c.want)
		}
	}
}

// Folding must agree with the matcher this replaced, or a query would change
// which sessions it lists - so the expectation is that matcher, not a hand-written
// yes or no.
func TestIndexFoldFolding(t *testing.T) {
	const s = "Deploying the CAFÉ egress fix"
	for _, q := range []string{"deploying", "DEPLOYING", "café", "CAFÉ", "egress fix", "É eGrEsS", "kubernetes"} {
		want := strings.Contains(strings.ToLower(s), strings.ToLower(q))
		if got, _ := indexFold(s, q); (got >= 0) != want {
			t.Errorf("indexFold(%q) hit = %v, but strings.ToLower matching says %v", q, got >= 0, want)
		}
	}
}

// A query is answered by one entry, not by the transcript with its entries run
// together. The only queries that changes are the ones carrying a newline.
func TestMatchesQueryIsPerEntry(t *testing.T) {
	th := Thread{Entries: []Entry{{Text: "first turn"}, {Text: "second turn"}}}
	for _, q := range []string{"first turn", "second", "TURN"} {
		if !query(q).MatchesQuery(th) {
			t.Errorf("MatchesQuery(%q) = false, want true", q)
		}
	}
	if query("absent").MatchesQuery(th) {
		t.Error("MatchesQuery matched an absent query")
	}
	if !query("").MatchesQuery(th) {
		t.Error("an empty query must match everything")
	}
	if query("first turn\nsecond").MatchesQuery(th) {
		t.Error("a query spanning two entries matched; per-entry matching should refuse it")
	}
}

// The row quotes the first place the session says the thing, and keeps Preview
// pointing at the opening message either way.
func TestSummarizeAttachesTheFirstMatch(t *testing.T) {
	th := Thread{Entries: []Entry{
		{Kind: KindMessage, Role: RoleUser, Text: "start the egress work"},
		{Kind: KindMessage, Role: RoleAssistant, Text: "egress done"},
	}}
	s := query("egress").Summarize(th)
	if s.Match == nil {
		t.Fatal("Summarize attached no match")
	}
	if s.Match.Text != th.Entries[0].Text {
		t.Errorf("match = %q, want the first entry %q", s.Match.Text, th.Entries[0].Text)
	}
	if s.Match.Role != RoleUser {
		t.Errorf("match role = %q, want %q", s.Match.Role, RoleUser)
	}
	if s.Preview != th.Entries[0].Text {
		t.Errorf("Preview = %q, want the opening message: a match must not overwrite it", s.Preview)
	}
	if plain := (ListOptions{}).Summarize(th); plain.Match != nil {
		t.Errorf("a listing with no query carried a match: %+v", plain.Match)
	}
}
