// Command catchup converts local agent conversation history into compact handover
// output. Usage: catchup <provider>[/<rank>] [flags].
//
// main is intentionally tiny: it resolves the environment into values and wires
// the layers together. All behavior lives behind cli.Run so that the program is
// exercised in tests without a process boundary.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wilbeibi/catchup/internal/cli"
	"github.com/wilbeibi/catchup/internal/session"
)

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

	if err := cli.Run(ctx, os.Args[1:], roots, cwd, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "catchup:", err)
		os.Exit(1)
	}
}
