// Command catchup converts local agent conversation history into compact handover
// output. Usage: catchup <agent>[/<rank>] [flags].
//
// main is intentionally tiny: it resolves the environment into values and wires
// the layers together. All behavior lives behind cli.Run so that the program is
// exercised in tests without a process boundary.
package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"

	"github.com/wilbeibi/catchup/internal/cli"
	"github.com/wilbeibi/catchup/internal/session"
)

//go:embed SKILL.md
var skillMD []byte

// version is stamped by goreleaser (-X main.version=...) on release builds.
var version = "dev"

func main() {
	ctx := context.Background()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "catchup:", err)
		os.Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "" // fall back to no directory filtering
	}

	roots := session.ResolveRoots(os.Getenv, home)
	current := session.ResolveCurrent(os.Getenv)
	skillDirs := session.ResolveSkillDirs(roots, home)

	if err := cli.Run(ctx, os.Args[1:], roots, current, skillDirs, skillMD, version, cwd, os.Stdin, os.Stdout, os.Stderr); err != nil {
		// A fork replaces this terminal's occupant with another agent, so
		// that agent's exit status is the run's answer and is passed through
		// unannounced: it has already reported itself, and a wrapper reading
		// the code needs the number it actually returned.
		var exit *cli.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.Code)
		}
		fmt.Fprintln(os.Stderr, "catchup:", err)
		os.Exit(1)
	}
}
