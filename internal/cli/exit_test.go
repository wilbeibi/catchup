package cli

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// exitCmd is a command that exits with code and nothing else — the shortest
// way to get a genuine *exec.ExitError from the OS rather than a hand-built
// stand-in that could not prove the conversion works.
func exitCmd(code int) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/c", "exit", strconv.Itoa(code))
	}
	return exec.Command("sh", "-c", "exit "+strconv.Itoa(code))
}

func TestLaunchErrorCarriesTheAgentsExitCode(t *testing.T) {
	var exit *ExitError
	got := launchError("codex", exitCmd(3).Run(), io.Discard)
	if !errors.As(got, &exit) {
		t.Fatalf("a launched agent's non-zero exit must survive as an ExitError, got %v", got)
	}
	if exit.Code != 3 {
		t.Errorf("want the agent's own code 3, got %d", exit.Code)
	}
}

func TestLaunchErrorLeavesCatchupsOwnFailuresAlone(t *testing.T) {
	// Failing to start the agent at all is catchup's error to report: there
	// is no exit status, because nothing ran.
	notFound := exec.Command("catchup-no-such-agent-binary").Run()
	if notFound == nil {
		t.Fatal("expected the missing binary to fail")
	}
	if got := launchError("codex", notFound, io.Discard); !errors.Is(got, notFound) {
		t.Errorf("a failure to launch must keep its own message, got %v", got)
	}
	// A clean exit is not an error, and must not become one.
	if got := launchError("codex", exitCmd(0).Run(), io.Discard); got != nil {
		t.Errorf("want nil for a successful agent, got %v", got)
	}
}

func TestLaunchErrorReportsASignalledAgentTheWayAShellWould(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no signals on Windows; a terminated process still carries an exit code")
	}
	var errOut bytes.Buffer
	var exit *ExitError
	killed := exec.Command("sh", "-c", "kill -TERM $$").Run()
	got := launchError("codex", killed, &errOut)
	if !errors.As(got, &exit) {
		t.Fatalf("a signalled agent must still carry a status out, got %v", got)
	}
	// 128+SIGTERM, the number a shell reports for the same death.
	if exit.Code != 143 {
		t.Errorf("want 143 for SIGTERM, got %d", exit.Code)
	}
	// The agent said nothing before dying, so the line has to name it — and
	// must not read as a catchup failure.
	if line := errOut.String(); !strings.Contains(line, "codex died: terminated") {
		t.Errorf("want the death attributed to the agent, got %q", line)
	}
}
