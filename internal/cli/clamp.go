package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/wilbeibi/catchup/internal/session"
)

// Entry clamping: a single oversized entry — almost always a pasted log,
// stack trace, or blob — keeps its head and tail with one marker line
// between them. The thresholds are fixed on purpose: no tunables and no
// content classification. Position beats statistics (people put their own
// words at the edges of a paste and the blob in the middle), and a clamp is
// recoverable where a deletion is not — the marker says exactly how to get
// the full text back.
const (
	clampMaxBytes  = 4096 // entries at or under this render whole
	clampHeadBytes = 2048
	clampTailBytes = 1024
)

// clampEntries returns t with every oversized entry reduced to head + marker
// + tail. Entries are copied on first change, so the caller's thread is
// never mutated. It is applied by the cli, never by the renderer: --json
// stays faithful and --full skips it, and those are cli decisions.
func clampEntries(t session.Thread) session.Thread {
	var out []session.Entry
	for i, e := range t.Entries {
		clamped, ok := clampText(e.Text)
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
// within clampMaxBytes. Cuts land on line boundaries when the window has
// any, and never split a UTF-8 rune.
func clampText(text string) (string, bool) {
	if len(text) <= clampMaxBytes {
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
