//go:build !windows

package cli

import (
	"io"
	"os"
)

// openTTY reopens the controlling terminal. It becomes the launched agent's
// stdin when --from - consumed the pipe; a var so tests can stand in a fake
// terminal.
var openTTY = func() (io.ReadCloser, error) { return os.Open("/dev/tty") }
