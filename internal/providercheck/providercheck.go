// Package providercheck is a shared conformance check every provider test runs.
//
// It encodes the invariants that are cheap to break and expensive to notice: a
// provider that lists a session must read it back; a read must produce a
// coherent timeline; and a format that records timestamps, tool failures, or
// compaction seams must surface them rather than quietly dropping them. The
// gate test here fails if a registered provider never calls Check, so a new
// provider cannot ship without declaring what its format supports. See
// docs/providers.md.
package providercheck

import (
	"context"
	"strings"
	"testing"
	"unicode"

	"github.com/wilbeibi/catchup/internal/session"
)

// Expect declares what the provider's fixture demonstrates. A true flag is an
// assertion about the format: the fixture must show it. A false flag means the
// fixture does not exercise it (typically because the format does not record
// it), and is the only place a provider is allowed to say so.
type Expect struct {
	Timestamps bool // entries carry a per-message time
	Failures   bool // the format records failed tool calls
	Compaction bool // the format records compaction seams
}

// Check runs the conformance invariants against a provider's own fixture. It
// exercises up to checkRows listed sessions and aggregates the capabilities, so
// a fixture with several sessions does not have to repeat each one.
func Check(t *testing.T, prov session.Provider, roots session.Roots, exp Expect) {
	t.Helper()
	ctx := context.Background()

	rows, err := prov.List(ctx, roots, session.ListOptions{})
	if err != nil {
		t.Fatalf("providercheck: List: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("providercheck: List returned no sessions")
	}
	for i, r := range rows {
		if r.Ref.Provider == "" {
			t.Errorf("providercheck: row %d has no provider", i)
		}
		if r.Rank != i+1 {
			t.Errorf("providercheck: row %d rank = %d, want %d", i, r.Rank, i+1)
		}
	}
	if _, err := prov.Resolve(ctx, roots, "providercheck-no-such-session"); err == nil {
		t.Errorf("providercheck: Resolve of an unknown id succeeded")
	}

	var failure, compact, haveUser, sawEntries bool
	var queryID, query string
	for _, r := range rows[:min(len(rows), checkRows)] {
		src, err := prov.Resolve(ctx, roots, r.Ref.SessionID)
		if err != nil {
			t.Fatalf("providercheck: Resolve(%q): %v", r.Ref.SessionID, err)
		}
		if src.Ref.SessionID != r.Ref.SessionID {
			t.Errorf("providercheck: resolved %q, want %q", src.Ref.SessionID, r.Ref.SessionID)
		}
		th, err := prov.Read(ctx, src)
		if err != nil {
			t.Fatalf("providercheck: Read(%q): %v", r.Ref.SessionID, err)
		}
		if len(th.Entries) == 0 {
			t.Errorf("providercheck: Read(%q) returned no entries", r.Ref.SessionID)
			continue
		}
		sawEntries = true
		for i, e := range th.Entries {
			switch e.Kind {
			case session.KindMessage:
				if e.Role == "" {
					t.Errorf("providercheck: %s entry %d is a message with no role", r.Ref.SessionID, i)
				}
				if strings.TrimSpace(e.Text) == "" {
					t.Errorf("providercheck: %s entry %d is an empty message", r.Ref.SessionID, i)
				}
			case session.KindFailure:
				failure = true
			case session.KindCompact:
				compact = true
			}
			if exp.Timestamps && e.Time.IsZero() {
				t.Errorf("providercheck: %s entry %d (%s) has no timestamp; the format records them", r.Ref.SessionID, i, e.Kind)
			}
		}
		if !haveUser {
			if q := queryToken(th); q != "" {
				queryID, query, haveUser = r.Ref.SessionID, q, true
			}
		}
	}
	if !sawEntries {
		t.Fatalf("providercheck: no listed session produced a timeline")
	}
	if exp.Failures && !failure {
		t.Errorf("providercheck: no failure entry, but the format records failed tool calls")
	}
	if exp.Compaction && !compact {
		t.Errorf("providercheck: no compaction entry, but the format records compaction seams")
	}

	// A listing must answer the same question a read does: a word lifted from
	// the timeline must select the session it came from.
	if haveUser {
		qrows, err := prov.List(ctx, roots, session.ListOptions{Query: query})
		if err != nil {
			t.Fatalf("providercheck: List(query %q): %v", query, err)
		}
		found := false
		for _, r := range qrows {
			if r.Ref.SessionID == queryID {
				found = true
			}
		}
		if !found {
			t.Errorf("providercheck: query %q did not list %q", query, queryID)
		}
	}
}

// checkRows bounds how many listed sessions Check reads.
const checkRows = 8

// queryToken returns a searchable word from the first user message, or "" when
// the fixture has none long enough to be selective.
func queryToken(th session.Thread) string {
	for _, e := range th.Entries {
		if e.Kind != session.KindMessage || e.Role != session.RoleUser {
			continue
		}
		for _, field := range strings.Fields(e.Text) {
			word := strings.TrimFunc(field, func(r rune) bool {
				return !unicode.IsLetter(r) && !unicode.IsDigit(r)
			})
			if len([]rune(word)) >= 4 {
				return word
			}
		}
	}
	return ""
}
