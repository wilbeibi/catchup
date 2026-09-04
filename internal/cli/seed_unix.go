//go:build !windows

package cli

// seedPrompt renders the opening prompt for the launched agent. The
// transcript travels in the command line: the OS owns the argument ceiling,
// and exec refuses loudly when it is crossed — which seedInto translates
// into the trim hint.
func seedPrompt(s seed) (string, error) {
	return s.inlineLead + "\n\n" + s.body, nil
}

// hideDir is a no-op here: a leading dot is all it takes for ls and every
// file manager to leave the directory out of a default listing.
func hideDir(string) {}
