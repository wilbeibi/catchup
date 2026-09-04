//go:build windows

package cli

import (
	"io"
	"os"
)

// openTTY reopens the console for reading — the Windows half of the Unix
// /dev/tty reopen. CONIN$ is the console's own device name, so opening it
// yields a fresh handle to the real keyboard even though this process's stdin
// is the pipe that --from - already drained. A var so tests can stand in a
// fake terminal.
var openTTY = func() (io.ReadCloser, error) { return os.Open("CONIN$") }
