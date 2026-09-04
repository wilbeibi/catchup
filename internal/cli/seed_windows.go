//go:build windows

package cli

import (
	"fmt"
	"syscall"
)

// seedPrompt renders the opening prompt for the launched agent. On Windows
// the transcript cannot travel in the command line at any size: npm installs
// agents as .cmd shims, and cmd.exe cuts an argument at its first newline —
// silently, and with a successful exit. So the body is written beside the
// agent and the prompt names it.
func seedPrompt(s seed) (string, error) {
	path, err := writeSeedFile(s.dir, s.body)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(s.fileLead, path), nil
}

// hideDir keeps the seed directory out of Explorer, the file dialogs, and a
// bare `dir`. The leading dot in its name carries no meaning on Windows — it
// is an ordinary character there — so the hidden attribute has to be set, the
// same way Visual Studio hides the .vs directory it creates beside a
// solution. Failure is not worth reporting: a visible directory is untidy,
// not broken.
func hideDir(dir string) {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return
	}
	_ = syscall.SetFileAttributes(p, syscall.FILE_ATTRIBUTE_HIDDEN)
}
