package render

import (
	"strings"
	"unicode/utf8"

	"github.com/wilbeibi/catchup/internal/session"
)

// Session text is replayed verbatim from an agent's log, which records tool
// output exactly as it arrived: ANSI colour codes, an OSC 0 window-title
// change, an OSC 52 clipboard write, stray control bytes. Printed straight to a
// terminal those act on the reader's terminal instead of describing what
// happened, and an agent reading the same text gets escape noise. Every human
// and agent facing rendering therefore goes through StripControl; JSON does
// not, since encoding/json escapes the sequence-introducing characters itself
// and its consumers parse the text rather than display it.

const (
	escByte = 0x1b // ESC, the 7-bit introducer
	belByte = 0x07 // BEL, one of the two control-string terminators
	delRune = 0x7f

	// The C1 introducers, which a UTF-8 terminal reads as these runes.
	c1DCS = 0x90
	c1SOS = 0x98
	c1CSI = 0x9b
	c1OSC = 0x9d
	c1PM  = 0x9e
	c1APC = 0x9f
)

// StripControl removes terminal escape sequences and stray control characters
// from s, keeping only newline and tab. Carriage return goes with the rest: a
// bare CR returns the cursor to column zero, so text after it overwrites the
// line the reader just saw.
//
// The scan is over runes, never bytes: 0x80-0x9F are UTF-8 continuation bytes
// in the middle of CJK and emoji, and only a decoded rune in that range is a C1
// control. Valid text in means valid text out. s is returned unchanged, with no
// allocation, when there is nothing to strip — a listing runs this over every
// row of every session it walks.
func StripControl(s string) string {
	if !mayHaveControl(s) {
		return s
	}
	var b strings.Builder
	last, stripped := 0, false
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		n := sequenceLen(s[i:], r, size)
		if n == 0 {
			i += size
			continue
		}
		if !stripped {
			b.Grow(len(s))
			stripped = true
		}
		b.WriteString(s[last:i])
		i += n
		last = i
	}
	if !stripped {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// mayHaveControl is the fast path: a byte scan for anything that could begin a
// sequence or be a control. 0xC2 leads the two-byte encoding of every rune up
// to U+00BF, so it is the only lead byte a C1 control can have; the Latin-1
// punctuation that shares it costs a rescan that strips nothing.
func mayHaveControl(s string) bool {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\n' || c == '\t':
		case c < 0x20, c == delRune, c == 0xc2:
			return true
		}
	}
	return false
}

// sequenceLen returns how many bytes to drop at the head of s, whose first rune
// is r and size bytes long, or 0 to keep that rune.
func sequenceLen(s string, r rune, size int) int {
	switch r {
	case '\n', '\t':
		return 0
	case escByte:
		return escLen(s)
	case c1CSI:
		return size + csiLen(s[size:])
	case c1DCS, c1SOS, c1OSC, c1PM, c1APC:
		// An unterminated string loses its introducer only, as below.
		return size + stringLen(s[size:])
	}
	if r < 0x20 || r == delRune || (r >= 0x80 && r <= 0x9f) {
		return size
	}
	// A byte that is not part of a valid rune displays as nothing anyway, and
	// keeping it here could leave a lead byte that joins its neighbour into a
	// C1 control once the bytes between them go.
	if r == utf8.RuneError && size == 1 {
		return 1
	}
	return 0
}

// escLen measures a complete sequence starting at the ESC at the head of s, or
// returns 1 for a lone ESC. An ESC before a multibyte rune or at the end of the
// text introduces nothing, so only the ESC itself goes.
func escLen(s string) int {
	if len(s) < 2 {
		return 1
	}
	switch s[1] {
	case '[':
		return 2 + csiLen(s[2:])
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC
		return 2 + stringLen(s[2:])
	}
	// Everything else: ESC, any intermediates (0x20-0x2F), one final byte
	// (0x30-0x7E). This is the form of ESC c (full reset) and ESC ( B.
	i := 1
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		return i + 1
	}
	return 1
}

// csiLen measures a CSI body: parameter and intermediate bytes (0x20-0x3F)
// followed by one final byte (0x40-0x7E). A body cut short by the end of the
// text, or by a byte no terminal would accept there, ends where it stops, so
// the visible text after it survives.
func csiLen(s string) int {
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 0x20 && c <= 0x3f:
		case c >= 0x40 && c <= 0x7e:
			return i + 1
		default:
			return i
		}
	}
	return len(s)
}

// stringLen measures a control-string body (OSC, DCS, SOS, PM, APC) up to and
// including its terminator: BEL, ESC \, or the C1 ST. The body may not cross a
// newline or hold another ESC, and an unterminated one measures zero, so only
// the introducer is dropped and a truncated OSC cannot swallow the rest of a
// transcript. Whatever it was carrying stays visible as inert text.
func stringLen(s string) int {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case belByte:
			return i + 1
		case escByte:
			if i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
			return 0
		case '\n':
			return 0
		case 0xc2:
			if i+1 < len(s) && s[i+1] == 0x9c { // C1 ST
				return i + 2
			}
		}
	}
	return 0
}

// stripThread returns t with every string a renderer displays sanitised. Kind
// and Role are left alone: providers normalize those onto the closed set of
// constants in package session, so they never carry log text. Entries and
// metadata are copied rather than written through — Read hands out a Source
// whose map the provider may still hold.
func stripThread(t session.Thread) session.Thread {
	t.Source = stripSource(t.Source)
	t.Excerpt = StripControl(t.Excerpt)
	t.Warnings = stripEach(t.Warnings)
	entries := make([]session.Entry, len(t.Entries))
	for i, e := range t.Entries {
		e.Tool = StripControl(e.Tool)
		e.Reason = StripControl(e.Reason)
		e.Text = StripControl(e.Text)
		entries[i] = e
	}
	t.Entries = entries
	return t
}

func stripSource(s session.Source) session.Source {
	s.Ref.SessionID = StripControl(s.Ref.SessionID)
	s.Path = StripControl(s.Path)
	s.Warnings = stripEach(s.Warnings)
	if s.Metadata != nil {
		meta := make(map[string]string, len(s.Metadata))
		for k, v := range s.Metadata {
			meta[StripControl(k)] = StripControl(v)
		}
		s.Metadata = meta
	}
	return s
}

func stripSummaries(rows []session.Summary) []session.Summary {
	out := make([]session.Summary, len(rows))
	for i, s := range rows {
		s.Ref.SessionID = StripControl(s.Ref.SessionID)
		s.Title = StripControl(s.Title)
		s.Cwd = StripControl(s.Cwd)
		s.Parent = StripControl(s.Parent)
		s.Relationship = StripControl(s.Relationship)
		s.AgentRole = StripControl(s.AgentRole)
		s.Preview = StripControl(s.Preview)
		if s.Match != nil {
			m := *s.Match
			m.Text = StripControl(m.Text)
			s.Match = &m
		}
		out[i] = s
	}
	return out
}

func stripEach(ss []string) []string {
	if len(ss) == 0 {
		return ss
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = StripControl(s)
	}
	return out
}
