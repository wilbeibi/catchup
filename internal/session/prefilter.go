package session

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A listing filter is answered in two steps, and both live here rather than in
// each provider: a provider knows where its agent keeps history and how to parse
// it, not how a query is matched.
//
// MayMatch is the cheap step. It reads a session's raw on-disk bytes and decides
// misses only, never matches: false means this session cannot match, true means
// "parse and confirm", and every uncertainty resolves to true. It exists because
// listing a query is dominated by parsing, not by reading, so a provider that
// parses every session before filtering does the expensive half of the work for
// sessions it is about to discard. Providers already use this shape to resolve an
// id from a filename before confirming against the parsed metadata.
//
// Matches is the confirming step, applied to the parsed Thread.
//
// raw is only read, never rewritten, so a caller that already holds a session's
// bytes can hand the same slice to its parser instead of reading the file twice.
func (o ListOptions) MayMatch(raw []byte) bool {
	return o.MayMatchCwd(raw) && o.MayMatchQuery(raw)
}

// MayMatchCwd and MayMatchQuery are the halves of MayMatch, for a provider that
// answers one of them another way: one whose format records the working
// directory in a separate index cannot read it out of a transcript's bytes, and
// one that synthesizes visible text during parsing cannot let these bytes decide
// the keyword half alone.
func (o ListOptions) MayMatchCwd(raw []byte) bool {
	return o.Cwd == "" || mayHaveCwd(raw, o.Cwd)
}

func (o ListOptions) MayMatchQuery(raw []byte) bool {
	return o.Query == "" || mayHaveText(raw, o.Query)
}

// Matches reports whether a parsed thread satisfies the filter. It is the
// authority MayMatch defers to, so a false positive there costs a parse and
// nothing more.
func (o ListOptions) Matches(t Thread) bool {
	return o.MatchesCwd(t.Source.Metadata["cwd"]) && o.MatchesQuery(t)
}

// MatchesCwd reports whether a session recorded in dir satisfies the directory
// filter. Providers whose format exposes the directory without a full parse call
// this early; Matches applies it again, which is idempotent.
func (o ListOptions) MatchesCwd(dir string) bool {
	return o.Cwd == "" || SameDir(dir, o.Cwd)
}

// MatchesQuery reports whether a thread satisfies the keyword filter: a literal,
// case-insensitive substring match against one entry's text.
//
// One entry's, not the whole transcript's: a query is answered by the passage
// that contains it, and a listing shows that passage. The only query this
// refuses that a match against the joined text would accept is one carrying a
// newline, which is the only kind that can span two entries.
func (o ListOptions) MatchesQuery(t Thread) bool {
	if o.Query == "" {
		return true
	}
	return o.firstMatch(t) != nil
}

// containsFold reports whether raw contains needle, comparing A-Z case-blind.
// The needle is folded here rather than by contract: it is a handful of bytes
// against a haystack of megabytes, so folding it per call costs nothing, and an
// unfolded needle would otherwise match nothing and be reported as a confident
// miss - the one answer this file must never give.
//
// Folding is applied to the few candidate windows rather than to raw, which
// leaves the caller's bytes intact and skips a write pass over a corpus that
// runs to hundreds of megabytes. Only A-Z folds: bytes above ASCII are compared
// as they are, which keeps a UTF-8 stream intact because continuation bytes are
// all >= 0x80 and never collide with the A-Z range. Non-ASCII letters therefore
// keep their case, and mayHaveText refuses to prefilter a query that depends on
// folding them.
//
// The two passes are independent because the question is only whether some
// position matches, not which. Each cursor advances monotonically, so a pass
// costs one traversal of raw however many candidates it rejects.
func containsFold(raw []byte, want string) bool {
	needle := asciiLower(want)
	n := len(needle)
	if n == 0 {
		return true
	}
	if n > len(raw) {
		return false
	}
	limit := len(raw) - n + 1
	lower := needle[0]
	upper := lower
	if 'a' <= lower && lower <= 'z' {
		upper = lower - ('a' - 'A')
	}
	if scanFold(raw, needle, limit, lower) {
		return true
	}
	return upper != lower && scanFold(raw, needle, limit, upper)
}

// scanFold walks every position below limit where raw holds first, and reports
// whether the window starting there folds equal to needle.
func scanFold(raw []byte, needle string, limit int, first byte) bool {
	for i := 0; i < limit; i++ {
		j := bytes.IndexByte(raw[i:limit], first)
		if j < 0 {
			return false
		}
		i += j
		if equalFoldASCII(raw[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

// equalFoldASCII compares a window against an already-lowercased needle.
func equalFoldASCII(window []byte, needle string) bool {
	for i := 0; i < len(needle); i++ {
		c := window[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != needle[i] {
			return false
		}
	}
	return true
}

// asciiLower folds A-Z in a string, the needle-side counterpart of containsFold.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// mayHaveCwd reports whether raw could belong to a session whose recorded
// working directory SameDir-matches cwd.
//
// SameDir compares filepath.Clean'd paths, and Clean only drops separators and
// "." / ".." elements, so every spelling that Cleans to cwd still contains cwd's
// final element preceded by a separator. That is the necessary condition, and
// the cleaned path itself is tried first because it is far more selective. The
// third probe is what carries Windows, where a stored path's backslashes reach
// the file JSON-escaped as "\\" and the cleaned form does not appear verbatim.
//
// Only the final element and the separator in front of it are checked against the
// file's escapes; an escaped parent element costs at most the first, more
// selective probe.
func mayHaveCwd(raw []byte, cwd string) bool {
	clean := filepath.Clean(cwd)
	base := filepath.Base(clean)
	if base == "" || base == "." || base == "/" || base == string(filepath.Separator) {
		return true // a root or relative directory carries no useful condition
	}
	if unfindable(base) {
		return true
	}
	if containsFold(raw, clean) || containsFold(raw, "/"+base) {
		return true
	}
	// A Windows path reaches the file JSON-escaped, so each separator is written
	// as a doubled backslash; the single byte is a substring of that either way.
	if runtime.GOOS == "windows" && containsFold(raw, "\\"+base) {
		return true
	}
	// Every probe above searches for a separator byte, and \u002f or \u005c erases
	// it - unlike \/, which keeps it - so both separators join the escape check.
	// Neither is filtered by host: on Unix an escaped backslash is not a separator
	// and costs one deferral, which no recorded path in this corpus pays.
	return escapeHides(raw, `/\`+base)
}

// mayHaveText reports whether raw could contain q in a session's visible text.
//
// Visible text is decoded from JSON strings in raw and providers concatenate the
// pieces with newlines, so a query that survives JSON encoding unchanged appears
// verbatim in the file whenever it appears in the text. Providers only ever strip
// text they decoded, and stripping keeps the visible text a subset of the file's
// bytes, which can cost a false positive but never a miss. A provider that
// synthesizes visible text of its own breaks that subset relation and must say so
// rather than let these bytes answer for it.
func mayHaveText(raw []byte, q string) bool {
	if unfindable(q) {
		return true
	}
	if containsFold(raw, q) {
		return true
	}
	// \/ keeps the separator byte but splits the query around it, so containsFold
	// misses a query spanning one. mayHaveCwd needs no such rule: it searches for a
	// separator and one element, which \/ still contains.
	if strings.ContainsRune(q, '/') && bytes.Contains(raw, []byte(`\/`)) {
		return true
	}
	return escapeHides(raw, q)
}

// unfindable reports whether want can never be compared against raw bytes at all,
// whatever the file contains.
func unfindable(want string) bool {
	for _, r := range want {
		// A cased non-ASCII rune needs Unicode folding, which containsFold does
		// not do, so it cannot be compared bytewise.
		if r >= utf8.RuneSelf {
			if unicode.ToLower(r) != r || unicode.ToUpper(r) != r {
				return true
			}
			continue
		}
		// These three are escaped by every writer, so the file never holds them
		// verbatim: a literal " arrives as \" and the needle straddles the pair.
		if c := byte(r); c < 0x20 || c == '"' || c == '\\' {
			return true
		}
	}
	return false
}

// escapeHides reports whether raw escapes any character of want, which is the
// only way want can be present in the decoded text yet absent from these bytes.
// Only want's characters matter and not their order, so a caller holding no
// single needle may pass the set of characters it cares about.
//
// It reads what the file actually escaped rather than assuming what its writer
// would escape. JSON permits \uXXXX for any character, so a catalog of the
// characters encoders are known to escape - Go escapes < > &, an ASCII-safe
// encoder escapes everything above ASCII - is a guess that fails silently the
// first time a writer escapes something outside it, and a miss here is invisible:
// the caller is told the session does not exist. Treating the mere presence of an
// escape as poisoning is sound but costs almost the whole prefilter, because a
// third of this corpus's files carry an escape and they hold 85% of its bytes.
//
// It runs only after containsFold has already failed, so it walks a file the
// caller is otherwise about to reject.
func escapeHides(raw []byte, want string) bool {
	var ascii ['\u007f' + 1]bool
	nonASCII := false
	for _, r := range want {
		switch {
		case r >= utf8.RuneSelf:
			nonASCII = true
		case 'a' <= r && r <= 'z':
			ascii[r], ascii[r-('a'-'A')] = true, true
		case 'A' <= r && r <= 'Z':
			ascii[r], ascii[r+('a'-'A')] = true, true
		default:
			ascii[r] = true
		}
	}
	for i := 0; ; {
		j := bytes.Index(raw[i:], []byte(`\u`))
		if j < 0 {
			return false
		}
		i += j + 2
		if i+4 > len(raw) {
			return true // truncated escape; assume the worst
		}
		v, err := strconv.ParseUint(string(raw[i:i+4]), 16, 32)
		if err != nil {
			return true
		}
		if r := rune(v); r >= utf8.RuneSelf {
			if nonASCII {
				return true
			}
		} else if ascii[r] {
			return true
		}
		i += 4
	}
}
