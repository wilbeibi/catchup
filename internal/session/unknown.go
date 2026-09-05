package session

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// UnknownTypes collects the record types a provider met and did not recognize.
//
// Every provider dispatches on a record type, and most of those types are
// deliberately not on the timeline — tool plumbing, telemetry, UI events. A
// provider therefore names both the types it acts on and the types it drops on
// purpose; whatever is left over lands here and becomes one warning. That is
// the difference between a transcript that is short because the session was
// short and one that is short because the agent's log grew a shape we have
// never looked at: eleven formats change without asking us, and a silent skip
// is the one thing that is not faithful.
type UnknownTypes struct {
	counts map[string]int
	total  int
}

// Add records one skipped record of the named type.
func (u *UnknownTypes) Add(name string) {
	if u.counts == nil {
		u.counts = map[string]int{}
	}
	if name == "" {
		name = "(untyped)"
	}
	u.counts[name]++
	u.total++
}

// maxNamed bounds the warning: enough names to recognize the change, few
// enough to stay one readable line.
const maxNamed = 5

// AppendTo adds the warning naming what was skipped, and returns the list
// unchanged when every record was recognized.
func (u *UnknownTypes) AppendTo(warnings []string) []string {
	if u.total == 0 {
		return warnings
	}
	names := make([]string, 0, len(u.counts))
	for name := range u.counts {
		names = append(names, name)
	}
	sort.Strings(names)

	shown := names
	var more string
	if len(shown) > maxNamed {
		shown = shown[:maxNamed]
		more = fmt.Sprintf(", and %d more", len(names)-maxNamed)
	}

	kinds := fmt.Sprintf("%d unrecognized types", len(names))
	if len(names) == 1 {
		kinds = "an unrecognized type"
	}
	records := fmt.Sprintf("%d records", u.total)
	if u.total == 1 {
		records = "1 record"
	}
	return append(warnings, fmt.Sprintf("skipped %s of %s (%s%s)",
		records, kinds, strings.Join(shown, ", "), more))
}

// ReadStopWarning explains why a provider stopped reading a log early. A value
// cut off at end of file is the ordinary case rather than damage — catchup
// reads files the agent is still appending to — so it says so, and the reader
// keeps trusting the records that came before it.
func ReadStopWarning(err error) string {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "the last record is incomplete (the agent may still be writing this session)"
	}
	return "stopped reading at a malformed record"
}
