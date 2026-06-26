// Command baton converts local agent conversation history into compact handover
// output. Usage: baton <provider>[/<rank>] [flags].
//
// main is intentionally tiny: it resolves the environment into values and wires
// the layers together. All behavior lives behind cli.Run so that the program is
// exercised in tests without a process boundary.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wilbeibi/baton/internal/cli"
	"github.com/wilbeibi/baton/internal/session"
)

func main() {
	ctx := context.Background()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "baton:", err)
		os.Exit(1)
	}

	roots := session.ResolveRoots(os.Getenv, home)

	if err := cli.Run(ctx, os.Args[1:], roots, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "baton:", err)
		os.Exit(1)
	}
}
