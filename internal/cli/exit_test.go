package cli

import (
	"errors"
	"os/exec"
	"runtime"
	"strconv"
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
	got := launchError(exitCmd(3).Run())
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
	if got := launchError(notFound); !errors.Is(got, notFound) {
		t.Errorf("a failure to launch must keep its own message, got %v", got)
	}
	// A clean exit is not an error, and must not become one.
	if got := launchError(exitCmd(0).Run()); got != nil {
		t.Errorf("want nil for a successful agent, got %v", got)
	}
}
