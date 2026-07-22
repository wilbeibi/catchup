package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/wilbeibi/catchup/internal/session"
)

// Entry clamping: a single oversized entry keeps its head and tail with one
// marker line between them, and the clamp is recoverable where a deletion is
// not — the marker says exactly how to get the full text back.
//
// The threshold splits by who wrote the entry, because "position beats
// statistics" holds for one author and not the other. A user message is
// usually a pasted log, stack trace, or blob: the person's own words sit at
// the edges and the blob in the middle, so head+tail is the right cut, and a
// small ceiling is right. An assistant message (and a compaction summary) is
// generated prose whose tables, numbers, and conclusions sit in the *middle* —
// head+tail is exactly wrong there — so it renders whole up to a far higher
// ceiling that still catches a pathological inline blob (an echoed file, a
// dumped payload). That ceiling (32 KiB) is an intentionally chosen policy
// threshold, not a derived one: above it, an entry is assumed a dumped payload
// rather than prose. No tunables, no content sniffing: the only signal is role.
const (
	clampPastedMaxBytes    = 4096  // user messages: pasted content, head+tail is right
	clampGeneratedMaxBytes = 32768 // assistant + compaction: prose kept whole; chosen policy ceiling, not derived
	clampHeadBytes         = 2048
	clampTailBytes         = 1024
)

// clampMax is the byte ceiling above which an entry is clamped, chosen by
// author: only a user message carries the pasted blobs the head+tail cut was
// designed for. Everything else — assistant turns, compaction summaries — is
// generated prose and gets the high ceiling.
func clampMax(e session.Entry) int {
	if e.Kind == session.KindMessage && e.Role == session.RoleUser {
		return clampPastedMaxBytes
	}
	return clampGeneratedMaxBytes
}

// clampEntries returns t with every oversized entry reduced to head + marker
// + tail. Entries are copied on first change, so the caller's thread is
// never mutated. It is applied by the cli, never by the renderer: --json
// stays faithful and --full skips it, and those are cli decisions.
func clampEntries(t session.Thread) session.Thread {
	var out []session.Entry
	for i, e := range t.Entries {
		clamped, ok := clampText(e.Text, clampMax(e))
		if !ok {
			if out != nil {
				out = append(out, e)
			}
			continue
		}
		if out == nil {
			out = append(out, t.Entries[:i]...)
		}
		e.Text = clamped
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
func clampText(text string, maxBytes int) (string, bool) {
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

	elided := text[headEnd:tailStart]
	marker := fmt.Sprintf("[... %d KB / %d lines elided; rerun with --full for the full text ...]",
		(len(elided)+1023)/1024, strings.Count(elided, "\n")+1)

	head := strings.TrimRight(text[:headEnd], "\n")
	tail := strings.TrimLeft(text[tailStart:], "\n")
	if tail == "" {
		return head + "\n\n" + marker, true
	}
	return head + "\n\n" + marker + "\n\n" + tail, true
}

// runeStart backs i off to the nearest UTF-8 rune boundary at or before it.
func runeStart(s string, i int) int {
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}
