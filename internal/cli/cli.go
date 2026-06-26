// Package cli is the wiring layer. It parses argv into a Command, dispatches to
// the selected Provider, and hands the result to the renderer. It owns no
// formatting and no history-reading logic of its own — it only sequences the
// other layers. The pipeline reads top to bottom: parse, locate, read, render.
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/wilbeibi/baton/internal/session"
	"github.com/wilbeibi/baton/internal/claude"
	"github.com/wilbeibi/baton/internal/codex"
	"github.com/wilbeibi/baton/internal/opencode"
	"github.com/wilbeibi/baton/internal/render"
)

// Run executes one invocation.
func Run(ctx context.Context, args []string, roots session.Roots, stdout, stderr io.Writer) error {
	cmd, err := Parse(args)
	if err != nil {
		return err
	}

	prov, err := selectProvider(cmd.Target.Provider)
	if err != nil {
		return err
	}

	if cmd.List {
		opts := session.ListOptions{Query: cmd.Target.Query, Limit: cmd.Limit}
		summaries, err := prov.List(ctx, roots, opts)
		if err != nil {
			return err
		}
		return render.List(stdout, cmd.Target.Provider, summaries)
	}

	src, err := locate(ctx, prov, roots, cmd)
	if err != nil {
		return err
	}

	if cmd.MetaOnly {
		return render.Meta(stdout, src, cmd.Format)
	}

	thread, err := prov.Read(ctx, src)
	if err != nil {
		return err
	}
	if cmd.LastN > 0 {
		thread = lastN(thread, cmd.LastN)
	}
	return render.Thread(stdout, thread, cmd.Format)
}

// selectProvider maps a provider name to its implementation. The set is closed
// at exactly three, so this is a switch, not a registry.
func selectProvider(name string) (session.Provider, error) {
	switch name {
	case session.ProviderCodex:
		return codex.New(), nil
	case session.ProviderClaude:
		return claude.New(), nil
	case session.ProviderOpenCode:
		return opencode.New(), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (want codex, claude, or opencode)", name)
	}
}

// locate chooses the right Provider resolution method for the command's
// selection mode. The precedence here mirrors the documented Target semantics:
// explicit id, then rank, then newest.
func locate(ctx context.Context, prov session.Provider, roots session.Roots, cmd Command) (session.Source, error) {
	switch {
	case cmd.Target.SessionID != "":
		return prov.Resolve(ctx, roots, cmd.Target.SessionID)
	case cmd.Target.Rank > 0:
		opts := session.ListOptions{Query: cmd.Target.Query, Limit: cmd.Limit}
		return prov.ResolveRank(ctx, roots, opts, cmd.Target.Rank)
	default:
		return prov.Resolve(ctx, roots, "")
	}
}

// lastN trims a thread to its final n entries, preserving the Source. It lives
// here, not in render, so the renderer stays a pure function of the Thread it
// is given.
func lastN(t session.Thread, n int) session.Thread {
	if n >= len(t.Entries) {
		return t
	}
	t.Entries = t.Entries[len(t.Entries)-n:]
	return t
}
