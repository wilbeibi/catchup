package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/wilbeibi/catchup/internal/session"
)

// Entry clamping: a single oversized entry keeps its head and tail with one
// marker line between them, and the clamp is recoverable where a deletion is
// not — the marker says exactly how to get the full text back.
//
// The threshold splits by who wrote the entry. A user message is usually a
// pasted log, stack trace, or blob: the person's own words sit at the edges
// and the blob in the middle, so head+tail is the right cut, and a small
// ceiling is right. An assistant message (and a compaction summary) is
// generated prose whose tables, numbers, and conclusions sit in the *middle* —
// head+tail is exactly wrong there — so it renders whole up to a far higher
// ceiling that still catches a pathological inline blob (an echoed file, a
// dumped payload). That ceiling (32 KiB) is an intentionally chosen policy
// threshold, not a derived one: above it, an entry is assumed a dumped payload
// rather than prose. No tunables, no content sniffing: the only signal is role.
//
// A failure's text is what a tool sent back — generated, so it takes the
// generated ceiling — and its input takes the pasted one: an input that large
// is a payload (a file being written), and its edges say what was tried.
const (
	clampPastedMaxBytes    = 4096
	clampGeneratedMaxBytes = 32768
	clampHeadBytes         = 2048
	clampTailBytes         = 1024
)

// A keyword read clamps the same way, with one exception: the word it was asked
// to find is never in the elided part. Without this the clamp runs before the
// reader ever sees the entry, so asking for a hit inside a pasted log returns a
// page that does not contain it and says nothing about why - and the same
// silence hides the hit from a downstream `rg`, which cannot search text that
// was already dropped. The budget bounds the context, never the match:
// Excerpt states the rule for a listing and this applies it to a read.
const (
	clampMatchBytes = 1024 // bytes of an elided middle kept around the match
	clampMatchLead  = 256  // how many of them are spent before it
)

// clampMax picks the byte ceiling by author; the design block above says why
// role is the only signal.
func clampMax(e session.Entry) int {
	if e.Kind == session.KindMessage && e.Role == session.RoleUser {
		return clampPastedMaxBytes
	}
	return clampGeneratedMaxBytes
}

// clampEntries returns t with every oversized entry reduced to head + marker
// + tail, its input included. Entries are copied on first change, so the
// caller's thread is never mutated. It is applied by the cli, never by the
// renderer: --json stays faithful and --full skips it, and those are cli
// decisions. A clamped input is re-encoded as a JSON string so Entry.Input
// stays valid JSON; only the text formats ever see it.
func clampEntries(t session.Thread, query string) session.Thread {
	var out []session.Entry
	for i, e := range t.Entries {
		text, textOK := clampText(e.Text, query, clampMax(e))
		// A tool call's input is not searched - MatchedEntries decides on Text
		// alone - so nothing there is under the match guarantee.
		input, inputOK := clampText(e.InputText(), "", clampPastedMaxBytes)
		if !textOK && !inputOK {
			if out != nil {
				out = append(out, e)
			}
			continue
		}
		if out == nil {
			out = append(out, t.Entries[:i]...)
		}
		if textOK {
			e.Text = text
		}
		if inputOK {
			encoded, _ := json.Marshal(input)
			e.Input = string(encoded)
		}
		out = append(out, e)
	}
	if out != nil {
		t.Entries = out
	}
	return t
}

// clampText reduces text to its first clampHeadBytes and last clampTailBytes
// around a marker naming what was elided; ok is false when text is already
// within maxBytes. Cuts land on line boundaries when the window has any, and
// never split a UTF-8 rune.
//
// When query is set and its first occurrence falls in the part being elided, a
// window around that occurrence is kept between two markers, so the reader who
// asked for the word gets the word plus what surrounds it rather than the two
// ends of a blob it is somewhere inside.
func clampText(text, query string, maxBytes int) (string, bool) {
	if len(text) <= maxBytes {
		return "", false
	}

	headEnd := clampHeadBytes
	if i := strings.LastIndexByte(text[:headEnd], '\n'); i > 0 {
		headEnd = i
	} else {
		headEnd = runeStart(text, headEnd)
	}

	tailStart := len(text) - clampTailBytes
	if i := strings.IndexByte(text[tailStart:], '\n'); i >= 0 {
		tailStart += i + 1
	} else {
		tailStart = runeStart(text, tailStart)
	}

	// A match that straddles a cut must live wholly on one side of it. Moving
	// the head cut to the match's start and the tail cut to its end keeps the
	// existing pieces independent: the separators between them can no longer
	// be inserted through the word the reader asked to see.
	if query != "" {
		start, end := session.IndexFold(text, query)
		if start >= 0 {
			if start < headEnd && end > headEnd {
				headEnd = start
			}
			if start < tailStart && end > tailStart {
				tailStart = end
			}
		}
	}

	head := text[:headEnd]
	tail := text[tailStart:]
	if query == "" {
		head = strings.TrimRight(head, "\n")
		tail = strings.TrimLeft(tail, "\n")
	}
	pieces := []string{head}
	if from, to, ok := matchWindow(text, query, headEnd, tailStart); ok {
		pieces = append(pieces,
			elision(text[headEnd:from]),
			text[from:to],
			elision(text[to:tailStart]))
	} else {
		pieces = append(pieces, elision(text[headEnd:tailStart]))
	}
	pieces = append(pieces, tail)

	kept := pieces[:0]
	for _, p := range pieces {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n"), true
}

// matchWindow returns the span of text to keep around query's first occurrence,
// or ok false when there is no occurrence or it already survives in the head or
// the tail. The span stays inside [headEnd, tailStart) - the part about to be
// elided - and always covers the whole match, however long the query is.
func matchWindow(text, query string, headEnd, tailStart int) (int, int, bool) {
	if query == "" {
		return 0, 0, false
	}
	start, end := session.IndexFold(text, query)
	if start < 0 || end <= headEnd || start >= tailStart {
		return 0, 0, false
	}
	from := runeStart(text, max(start-clampMatchLead, headEnd))
	to := min(from+clampMatchBytes, tailStart)
	if to < len(text) {
		to = runeStart(text, to)
	}
	return from, max(to, min(end, tailStart)), true
}

// elision is the marker standing in for a removed span, empty when nothing was
// removed.
func elision(cut string) string {
	if cut == "" {
		return ""
	}
	return fmt.Sprintf("[... %d KB / %d lines elided; rerun with --full for the full text ...]",
		(len(cut)+1023)/1024, strings.Count(cut, "\n")+1)
}

// runeStart backs i off to the nearest UTF-8 rune boundary at or before it.
func runeStart(s string, i int) int {
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}
