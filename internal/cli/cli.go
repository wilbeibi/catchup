// Package cli is the wiring layer. It parses argv into a Command, dispatches to
// the selected Provider, and hands the result to the renderer. It owns no
// formatting and no history-reading logic of its own — it only sequences the
// other layers. The pipeline reads top to bottom: parse, locate, read, render.
package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/wilbeibi/catchup/internal/claude"
	"github.com/wilbeibi/catchup/internal/codex"
	"github.com/wilbeibi/catchup/internal/opencode"
	"github.com/wilbeibi/catchup/internal/piagent"
	"github.com/wilbeibi/catchup/internal/render"
	"github.com/wilbeibi/catchup/internal/session"
)

const helpText = `Usage: catchup <provider>[/<rank>] [flags]

Providers: codex, claude, opencode, pi-agent

Flags:
  --list              list recent sessions
  -q, --query <text>  filter by keyword (implies --list)
  --id <id>           select by exact session id
  -I                  print metadata only, no messages
  --last <N>          show last N exchanges only
  --since-compact     show only the final compaction segment
  --json              output JSON
  --html              output HTML
  --md, --markdown    output Markdown (default)
  -n <N>              cap listing rows (default 20)
  -h, --help          print this help

Examples:
  catchup claude              latest Claude session → Markdown
  catchup claude --list       list recent Claude sessions
  catchup codex -q "deploy"   search Codex sessions by keyword
  catchup codex/3             3rd most recent Codex session
  catchup claude --last 5     last 5 exchanges
  catchup claude --since-compact  tail after last compaction
`

// Run executes one invocation. current maps a provider name to the id of the
// session we are running inside, when that agent injects one (see
// session.ResolveCurrent); it lets the default selection target the live
// session exactly rather than guessing by recency.
func Run(ctx context.Context, args []string, roots session.Roots, current map[string]string, cwd string, stdout, stderr io.Writer) error {
	cmd, err := Parse(args)
	if err != nil {
		return err
	}

	if cmd.Help {
		fmt.Fprint(stdout, helpText)
		return nil
	}

	prov, err := selectProvider(cmd.Target.Provider)
	if err != nil {
		return err
	}

	if cmd.List {
		opts := session.ListOptions{Query: cmd.Target.Query, Cwd: cwd, Limit: cmd.Limit}
		summaries, err := prov.List(ctx, roots, opts)
		if err != nil {
			return err
		}
		return render.List(stdout, cmd.Target.Provider, summaries)
	}

	src, err := locate(ctx, prov, roots, cmd, cwd, current)
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
	if cmd.SinceCompact {
		thread = sinceCompact(thread)
	}
	if cmd.LastN > 0 {
		thread = lastTurns(thread, cmd.LastN)
	}
	return render.Thread(stdout, thread, cmd.Format)
}

// selectProvider maps a provider name to its implementation. The set is closed
// and small, so this is a switch, not a registry.
func selectProvider(name string) (session.Provider, error) {
	switch name {
	case session.ProviderCodex:
		return codex.New(), nil
	case session.ProviderClaude:
		return claude.New(), nil
	case session.ProviderOpenCode:
		return opencode.New(), nil
	case session.ProviderPiAgent:
		return piagent.New(), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (want codex, claude, opencode, or pi-agent)", name)
	}
}

// locate chooses the right Provider resolution method for the command's
// selection mode. The precedence here mirrors the documented Target semantics:
// explicit id, then rank, then newest. When cwd is set, the newest and rank
// resolutions are scoped to sessions in that directory; --id bypasses the
// directory filter.
//
// In the default case (no explicit selector) an injected current-session id for
// this provider wins over "newest in cwd": only the agent that set it can tell
// its live session from another of its sessions sharing the directory, which
// recency cannot. Providers with no such signal fall through to newest-in-cwd.
func locate(ctx context.Context, prov session.Provider, roots session.Roots, cmd Command, cwd string, current map[string]string) (session.Source, error) {
	switch {
	case cmd.Target.SessionID != "":
		return prov.Resolve(ctx, roots, cmd.Target.SessionID)
	case cmd.Target.Rank > 0:
		opts := session.ListOptions{Query: cmd.Target.Query, Cwd: cwd, Limit: cmd.Limit}
		return prov.ResolveRank(ctx, roots, opts, cmd.Target.Rank)
	case current[cmd.Target.Provider] != "":
		return prov.Resolve(ctx, roots, current[cmd.Target.Provider])
	default:
		opts := session.ListOptions{Cwd: cwd, Limit: 1}
		return prov.ResolveRank(ctx, roots, opts, 1)
	}
}

// lastTurns trims a thread to its final n exchanges, preserving the Source. A
// turn begins at each user message, so this keeps everything from the
// n-th-from-last user message onward — the user's prompt plus every assistant
// reply (and any compaction markers) that follow it. With fewer than n user
// turns, the whole thread is kept. It lives here, not in render, so the
// renderer stays a pure function of the Thread it is given.
func lastTurns(t session.Thread, n int) session.Thread {
	count := 0
	for i := len(t.Entries) - 1; i >= 0; i-- {
		e := t.Entries[i]
		if e.Kind == session.KindMessage && e.Role == session.RoleUser {
			if count++; count == n {
				t.Entries = t.Entries[i:]
				return t
			}
		}
	}
	return t
}

// sinceCompact trims a thread to its final compaction segment: the last
// KindCompact entry and everything after it. On Claude that entry carries the
// summary of the pre-compaction context, so the result leads with a recap and
// continues with the live tail; on Codex and OpenCode the marker is empty, so
// it is a plain cut. When the thread has no compaction marker at all the whole
// thread is returned unchanged, which is what lets a caller (e.g. a skill)
// apply this unconditionally.
func sinceCompact(t session.Thread) session.Thread {
	for i := len(t.Entries) - 1; i >= 0; i-- {
		if t.Entries[i].Kind == session.KindCompact {
			t.Entries = t.Entries[i:]
			return t
		}
	}
	return t
}
