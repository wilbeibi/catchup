package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// seed is one prepared handoff: the instruction that opens the launched
// agent's session, and the transcript that instruction refers to.
//
// The two are kept apart because the channel between catchup and the agent
// is platform-owned. argv inlines the body after inlineLead; a file writes
// the body out and fileLead names where it went.
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
// the agent starts. The body digest makes the file immutable and lets
// identical handoffs share one retained copy.
//
// The file is kept, not cleaned up: the launched session's history names this
// path instead of carrying the transcript, so removing it would leave that
// session unreadable and a later native resume with nothing to find.
func writeSeedFile(dir, body string) (string, error) {
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

	digest := sha256.Sum256([]byte(body))
	name := fmt.Sprintf("seed-%x.md", digest)
	rel := filepath.Join(seedDirName, name)
	path := filepath.Join(dir, rel)

	// Publish a complete file in one rename so another simultaneous launch can
	// never observe a partial transcript. Platforms differ on whether Rename
	// replaces an existing destination; either result is safe because the full
	// digest means both writers have the same body.
	f, err := os.CreateTemp(out, ".seed-*.tmp")
	if err != nil {
		return "", fmt.Errorf("cannot write the seed file: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	_, werr := f.WriteString(body)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", fmt.Errorf("cannot write the seed file: %w", werr)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows refuses to replace the identical file another launch may have
		// published first. Reuse it only after confirming its content.
		existing, readErr := os.ReadFile(path)
		if readErr == nil && string(existing) == body {
			return rel, nil
		}
		return "", fmt.Errorf("cannot write the seed file: %w", err)
	}
	return rel, nil
}
