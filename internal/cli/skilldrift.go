package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The installed SKILL.md is the LLM-facing half of the interface, and it
// ships through a different channel than the binary: an upgrade replaces the
// binary but touches none of the copies install-skill wrote into agent skill
// directories. After a release the two can teach different grammars, and a
// stale skill fails silently — error hints rescue removed spellings, but
// nothing surfaces the features an old copy never mentions. So install-skill
// stamps each copy with the build version, and normal runs compare stamps.

const fmDelim = "---\n"

// splitFrontmatter splits md into the frontmatter body and the remainder
// starting at the closing "---" line. ok is false when md does not open with
// a frontmatter block.
func splitFrontmatter(md []byte) (body, rest []byte, ok bool) {
	if !bytes.HasPrefix(md, []byte(fmDelim)) {
		return nil, nil, false
	}
	after := md[len(fmDelim):]
	end := bytes.Index(after, []byte("\n---"))
	if end < 0 {
		return nil, nil, false
	}
	return after[:end+1], after[end+1:], true
}

// stampSkillVersion returns skillMD with "version: <version>" set in its YAML
// frontmatter, replacing any existing version line so re-installs stay
// idempotent. Content without a frontmatter block is returned unchanged.
func stampSkillVersion(skillMD []byte, version string) []byte {
	body, rest, ok := splitFrontmatter(skillMD)
	if !ok {
		return skillMD
	}
	var b bytes.Buffer
	b.WriteString(fmDelim)
	for _, line := range strings.SplitAfter(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "version:") {
			continue
		}
		b.WriteString(line)
	}
	fmt.Fprintf(&b, "version: %s\n", version)
	b.Write(rest)
	return b.Bytes()
}

// installedSkillVersion reads the version stamp of an installed SKILL.md.
// ok is false when the file is absent — absence is a choice, not drift.
// version is "" for a readable copy that predates stamping.
func installedSkillVersion(path string) (version string, ok bool) {
	md, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	body, _, has := splitFrontmatter(md)
	if !has {
		return "", true
	}
	for _, line := range strings.Split(string(body), "\n") {
		if v, found := strings.CutPrefix(line, "version:"); found {
			return strings.TrimSpace(v), true
		}
	}
	return "", true
}

// warnSkillDrift prints one stderr line when installed SKILL.md copies were
// written by a different release than the running binary, in either
// direction — a downgrade drifts too. Dev builds skip the check: their
// grammar has no release to match. The hint must stay on stderr; stdout is
// the wire format.
func warnSkillDrift(skillDirs map[string]string, version string, stderr io.Writer) {
	if version == "" || version == "dev" {
		return
	}
	var drifted []string
	for _, name := range providerNames() {
		dir, ok := skillDirs[name]
		if !ok {
			continue
		}
		v, ok := installedSkillVersion(filepath.Join(dir, "catchup", "SKILL.md"))
		if !ok || v == version {
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
