package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// seed is one prepared handoff: the instruction that opens the launched
// agent's session, and the transcript that instruction refers to.
//
// The two are kept apart because the channel between catchup and the agent
// is platform-owned (docs/DESIGN.md, D6b). argv inlines the body after
// inlineLead; a file writes the body out and fileLead names where it went.
// Each caller supplies both wordings, so the copy for one kind of fork stays
// in one place.
type seed struct {
	// inlineLead opens the prompt when the body travels in the command line.
	inlineLead string
	// fileLead opens it when the body travels beside the agent instead; it
	// carries exactly one %s, for the seed file's path, and must render to a
	// single line. The file channel exists because cmd.exe cuts an argument
	// at its first newline, so a multi-line fileLead reintroduces the very
	// truncation it was written to avoid.
	fileLead string
	body     string
	// trimHint names the oversize recovery in the caller's own grammar: a
	// store read re-runs with --last/--since-compact, an artifact is
	// re-rendered at its source.
	trimHint string
	// label names the source in the seed file's name, so a directory of kept
	// seeds stays browsable.
	label string
	// dir is the directory the agent starts in, and so where a file channel
	// writes: inside the agent's own workspace, which is the only place its
	// sandbox defaults let it read without a prompt.
	dir string
}

// seedDirName is where a file-channel seed is written, relative to the
// directory the launched agent starts in.
const seedDirName = ".catchup"

// writeSeedFile writes a seed body next to the agent that will read it and
// returns the path to name in the prompt — relative, because that is where
// the agent starts.
//
// The file is kept, not cleaned up: the launched session's history names this
// path instead of carrying the transcript, so removing it would leave that
// session unreadable and a later native resume with nothing to find
// (docs/DESIGN.md, D6b).
func writeSeedFile(dir, label, body string) (string, error) {
	out := filepath.Join(dir, seedDirName)
	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", fmt.Errorf("cannot write the seed file: %w; render the transcript yourself and seed from it: catchup <agent> --agent > s.md && catchup fork --into <agent> --from s.md", err)
	}
	// Both of these are best effort: the directory keeping out of the way is
	// a courtesy, and a seed the agent can read matters more than a clean
	// git status or a clean file listing.
	//
	// A .gitignore of "*" is how a tool-owned directory stays out of a
	// repository without catchup editing a .gitignore the user owns; pytest
	// settled on the same trick for .pytest_cache. The leading dot does the
	// rest on Unix, but names mean nothing to Explorer, so Windows needs the
	// hidden attribute set explicitly — which is what Visual Studio does with
	// its own .vs directory.
	_ = os.WriteFile(filepath.Join(out, ".gitignore"), []byte("*\n"), 0o644)
	hideDir(out)

	name := "seed-" + time.Now().Format("20060102-150405")
	if s := slug(label); s != "" {
		name += "-" + s
	}
	name += ".md"
	if err := os.WriteFile(filepath.Join(out, name), []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("cannot write the seed file: %w", err)
	}
	return filepath.Join(seedDirName, name), nil
}

// slug reduces a label to filename-safe characters so that a session id, a
// file path, and a URL can all name the seed they produced. Long labels are
// cut rather than rejected: the timestamp already makes the name unique.
func slug(label string) string {
	const max = 48
	var b strings.Builder
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
		if b.Len() >= max {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
