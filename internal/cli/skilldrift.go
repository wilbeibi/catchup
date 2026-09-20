package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wilbeibi/catchup/internal/session"
)

// The installed SKILL.md is the LLM-facing half of the interface, and it
// ships through a different channel than the binary: an upgrade replaces the
// binary but touches none of the copies install-skill wrote into agent skill
// directories. After a release the two can teach different grammars, and a
// stale skill fails silently — error hints rescue removed spellings, but
// nothing surfaces the features an old copy never mentions. So install-skill
// stamps each copy with the build version, and normal runs compare the text
// around that stamp: a release that left SKILL.md alone changed no grammar,
// and re-installing an identical file is not a fix worth asking for.

const fmDelim = "---\n"

// splitFrontmatter splits md into the frontmatter body and the remainder
// starting at the closing "---" line. ok is false when md does not open with
// a frontmatter block.
func splitFrontmatter(md []byte) (body, rest []byte, ok bool) {
	if !bytes.HasPrefix(md, []byte(fmDelim)) {
		return nil, nil, false
	}
	after := md[len(fmDelim):]
	// The closing delimiter is a whole "---" line: matching a bare "\n---"
	// would also hit a body line that merely starts with dashes.
	end := bytes.Index(after, []byte("\n---\n"))
	if end < 0 && bytes.HasSuffix(after, []byte("\n---")) {
		end = len(after) - len("\n---")
	}
	if end < 0 {
		return nil, nil, false
	}
	return after[:end+1], after[end+1:], true
}

// dropVersion splits md the way splitFrontmatter does, with any "version:"
// line removed from the frontmatter body — the one line install-skill owns.
func dropVersion(md []byte) (body, rest []byte, ok bool) {
	fm, rest, ok := splitFrontmatter(md)
	if !ok {
		return nil, nil, false
	}
	var b bytes.Buffer
	for _, line := range strings.SplitAfter(string(fm), "\n") {
		if line == "" || strings.HasPrefix(line, "version:") {
			continue
		}
		b.WriteString(line)
	}
	return b.Bytes(), rest, true
}

// stampSkillVersion returns skillMD with "version: <version>" set in its YAML
// frontmatter, replacing any existing version line so re-installs stay
// idempotent. Content without a frontmatter block is returned unchanged.
func stampSkillVersion(skillMD []byte, version string) []byte {
	body, rest, ok := dropVersion(skillMD)
	if !ok {
		return skillMD
	}
	var b bytes.Buffer
	b.WriteString(fmDelim)
	b.Write(body)
	fmt.Fprintf(&b, "version: %s\n", version)
	b.Write(rest)
	return b.Bytes()
}

// lf folds CRLF to LF: a checkout or editor on Windows rewrites line endings
// without changing a word, and a word is what the skill teaches.
func lf(md []byte) []byte {
	return bytes.ReplaceAll(md, []byte("\r\n"), []byte("\n"))
}

// skillBody returns everything about md that teaches grammar: the stamp line
// install-skill owns is dropped, line endings are normalised, and what is
// left is what two copies must share to say the same thing.
func skillBody(md []byte) []byte {
	md = lf(md)
	body, rest, ok := dropVersion(md)
	if !ok {
		return md
	}
	return append(append([]byte(fmDelim), body...), rest...)
}

// skillVersion reads the version stamp out of an installed SKILL.md. It is ""
// for a copy that predates stamping, or that has no frontmatter at all.
func skillVersion(md []byte) string {
	body, _, ok := splitFrontmatter(md)
	if !ok {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		if v, found := strings.CutPrefix(line, "version:"); found {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// warnSkillDrift prints one stderr line when installed SKILL.md copies both
// carry another release's stamp — in either direction, a downgrade drifts
// too — and actually read differently from the embedded skill. A copy the
// user edited keeps this build's stamp and is left alone: it is theirs, and
// the fix on offer would overwrite it. Dev builds skip the check: their
// grammar has no release to match. The hint must stay on stderr; stdout is
// the wire format.
func warnSkillDrift(skillDirs map[string]string, skillMD []byte, version string, stderr io.Writer) {
	if version == "" || version == "dev" {
		return
	}
	want := skillBody(skillMD)
	var drifted []string
	for _, name := range session.Providers {
		dir, ok := skillDirs[name]
		if !ok {
			continue
		}
		md, err := os.ReadFile(filepath.Join(dir, "catchup", "SKILL.md"))
		if err != nil {
			continue // absence is a choice, not drift
		}
		md = lf(md)
		v := skillVersion(md)
		if v == version || bytes.Equal(skillBody(md), want) {
			continue
		}
		if v == "" {
			v = "unstamped"
		}
		drifted = append(drifted, name+" "+v)
	}
	if len(drifted) > 0 {
		fmt.Fprintf(stderr, "skill drift: installed SKILL.md (%s) does not match binary %s; run: catchup install-skill\n",
			strings.Join(drifted, ", "), version)
	}
}
