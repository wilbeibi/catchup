package session

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestUnknownTypes(t *testing.T) {
	var none UnknownTypes
	if w := none.AppendTo(nil); w != nil {
		t.Errorf("nothing skipped: warnings = %q, want none", w)
	}

	var one UnknownTypes
	one.Add("widget.created")
	got := one.AppendTo([]string{"earlier"})
	if len(got) != 2 || got[0] != "earlier" {
		t.Fatalf("warnings = %q, want the earlier warning kept", got)
	}
	if want := "skipped 1 record of an unrecognized type (widget.created)"; got[1] != want {
		t.Errorf("warning = %q, want %q", got[1], want)
	}

	var many UnknownTypes
	for _, name := range []string{"b", "a", "b", ""} {
		many.Add(name)
	}
	w := many.AppendTo(nil)[0]
	if want := "skipped 4 records of 3 unrecognized types ((untyped), a, b)"; w != want {
		t.Errorf("warning = %q, want %q", w, want)
	}

	// More names than fit the line: the count stays exact, the list is capped.
	var lots UnknownTypes
	for _, name := range []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7"} {
		lots.Add(name)
	}
	w = lots.AppendTo(nil)[0]
	if !strings.Contains(w, "7 unrecognized types") || !strings.Contains(w, "and 2 more") {
		t.Errorf("warning = %q, want 7 types with 2 elided", w)
	}
	if strings.Contains(w, "t6") {
		t.Errorf("warning = %q, want at most %d names", w, maxNamed)
	}
}

func TestReadStopWarning(t *testing.T) {
	// A record cut off at end of file is the agent still writing, not damage.
	torn := json.NewDecoder(strings.NewReader(`{"type":"mess`)).Decode(new(struct{}))
	if !errors.Is(torn, io.ErrUnexpectedEOF) {
		t.Fatalf("a torn record decoded as %v, want io.ErrUnexpectedEOF", torn)
	}
	if w := ReadStopWarning(torn); !strings.Contains(w, "may still be writing") {
		t.Errorf("torn tail: %q, want the still-writing wording", w)
	}

	bad := json.NewDecoder(strings.NewReader(`{"type":]}`)).Decode(new(struct{}))
	if w := ReadStopWarning(bad); !strings.Contains(w, "malformed") {
		t.Errorf("malformed record: %q, want the malformed wording", w)
	}
}
