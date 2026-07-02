// Package cli is the wiring layer. It parses argv into a Command, dispatches to
// the selected Provider, and hands the result to the renderer. It owns no
// formatting and no history-reading logic of its own — it only sequences the
// other layers. The pipeline reads top to bottom: parse, locate, read, render.
package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/wilbeibi/catchup/internal/claude"
	"github.com/wilbeibi/catchup/internal/codex"
	"github.com/wilbeibi/catchup/internal/opencode"
	"github.com/wilbeibi/catchup/internal/piagent"
	"github.com/wilbeibi/catchup/internal/render"
	"github.com/wilbeibi/catchup/internal/session"
)

const helpText = `Usage: catchup [agent[/<rank>]] [flags]
       catchup fork [agent] [--into <agent>]
       catchup install-skill [agent]

Agents: codex, claude, opencode, pi-agent
Omit the agent to use whichever one has the newest session in this directory.

Flags:
  --list              list recent sessions
  -q, --query <text>  filter by keyword (implies --list)
  --id <id>           select by exact session id
  -i, --info          print metadata only, no messages
  --last <N>          show last N exchanges only
  --since-compact     show only the final compaction segment
  --json              output JSON
  --html              output HTML
  --md, --markdown    output Markdown (default)
  -n, --limit <N>     cap listing rows (default 20)
  --into <agent>      with fork: start a different agent, seeded with the transcript
  -h, --help          print this help

Examples:
  catchup                     latest session from any agent → Markdown
  catchup claude              latest Claude session → Markdown
  catchup claude --list       list recent Claude sessions
  catchup codex -q "deploy"   search Codex sessions by keyword
  catchup codex/3             3rd most recent Codex session
  catchup claude --last 5     last 5 exchanges
  catchup claude --since-compact  tail after last compaction
  catchup fork                fork the latest session in this directory
  catchup fork codex          fork the latest Codex session in this directory
  catchup fork codex --into claude  continue the Codex session in Claude
  catchup install-skill       install catchup's SKILL.md for every detected agent
  catchup install-skill codex install catchup's SKILL.md for Codex only
`

type forkRunner func(context.Context, session.Source, io.Reader, io.Writer, io.Writer) error

var runFork forkRunner = execFork

// Run executes one invocation. current maps a provider name to the id of the
// session we are running inside, when that agent injects one (see
// session.ResolveCurrent); it lets the default selection target the live
// session exactly rather than guessing by recency. skillDirs maps a provider
// name to its global Agent Skills directory (see session.ResolveSkillDirs)
// and skillMD is the SKILL.md content to install there; both are only used by
// the install-skill action.
func Run(ctx context.Context, args []string, roots session.Roots, current map[string]string, skillDirs map[string]string, skillMD []byte, cwd string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd, err := Parse(args)
	if err != nil {
		return err
	}

	if cmd.Help {
		fmt.Fprint(stdout, helpText)
		return nil
	}

	if cmd.Action == "fork" {
		src, err := locateForkSource(ctx, roots, cmd, cwd)
		if err != nil {
			return err
		}
		if cmd.Into != "" {
			return forkInto(ctx, src, cmd, stdin, stdout, stderr)
		}
		return runFork(ctx, src, stdin, stdout, stderr)
	}

	if cmd.Action == "install-skill" {
		return installSkill(cmd.Target.Provider, skillDirs, skillMD, stdout)
	}

	// With no agent named, the agent owning the newest session in cwd is the
	// target; the normal locate below then re-selects within that provider, so
	// --list, -q, and the trims all work against the detected agent.
	if cmd.Target.Provider == "" {
		src, err := newestAcross(ctx, roots, cwd)
		if err != nil {
			return err
		}
		cmd.Target.Provider = src.Ref.Provider
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

func providerNames() []string {
	return []string{
		session.ProviderCodex,
		session.ProviderClaude,
		session.ProviderOpenCode,
		session.ProviderPiAgent,
	}
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
		if name == "list" {
			return nil, fmt.Errorf(`unknown agent "list"; did you mean catchup --list?`)
		}
		return nil, fmt.Errorf("unknown agent %q (want codex, claude, opencode, or pi-agent); run catchup --help", name)
	}
}

func locateForkSource(ctx context.Context, roots session.Roots, cmd Command, cwd string) (session.Source, error) {
	if cmd.Target.Provider != "" {
		prov, err := selectProvider(cmd.Target.Provider)
		if err != nil {
			return session.Source{}, err
		}
		return newestInCwd(ctx, prov, roots, cmd.Target.Provider, cwd)
	}
	src, err := newestAcross(ctx, roots, cwd)
	if err != nil {
		return session.Source{}, fmt.Errorf("fork: %w", err)
	}
	return src, nil
}

// newestAcross finds the single newest session in cwd across every provider.
// It is what lets both a bare read and a bare fork omit the agent name.
func newestAcross(ctx context.Context, roots session.Roots, cwd string) (session.Source, error) {
	var latest session.Source
	var have bool
	var failures []error
	for _, name := range providerNames() {
		prov, err := selectProvider(name)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		src, err := newestInCwd(ctx, prov, roots, name, cwd)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !have || src.UpdatedAt.After(latest.UpdatedAt) {
			latest, have = src, true
		}
	}
	if have {
		return latest, nil
	}
	if len(failures) > 0 {
		return session.Source{}, fmt.Errorf("no sessions found in %q", cwd)
	}
	return session.Source{}, fmt.Errorf("no agents available")
}

// installSkill writes skillMD to "<dir>/catchup/SKILL.md" for one provider, or
// for every provider in providerNames() when provider is "". Providers with no
// entry in skillDirs are skipped rather than erroring, so the closed provider
// set and the skill-directory set can evolve independently.
func installSkill(provider string, skillDirs map[string]string, skillMD []byte, stdout io.Writer) error {
	names := providerNames()
	if provider != "" {
		if _, err := selectProvider(provider); err != nil {
			return err
		}
		names = []string{provider}
	}

	for _, name := range names {
		dir, ok := skillDirs[name]
		if !ok {
			continue
		}
		path := filepath.Join(dir, "catchup", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("install-skill %s: %w", name, err)
		}
		if err := os.WriteFile(path, skillMD, 0o644); err != nil {
			return fmt.Errorf("install-skill %s: %w", name, err)
		}
		fmt.Fprintf(stdout, "installed %s\n", path)
	}
	return nil
}

func newestInCwd(ctx context.Context, prov session.Provider, roots session.Roots, name, cwd string) (session.Source, error) {
	opts := session.ListOptions{Cwd: cwd, Limit: 1}
	src, err := prov.ResolveRank(ctx, roots, opts, 1)
	if err != nil {
		return session.Source{}, err
	}
	if src.Ref.Provider == "" {
		src.Ref.Provider = name
	}
	return src, nil
}

func execFork(ctx context.Context, src session.Source, stdin io.Reader, stdout, stderr io.Writer) error {
	name, args, err := forkCommand(src)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

type intoRunner func(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error

var runInto intoRunner = execInto

func execInto(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// forkInto is the cross-agent half of fork: it cannot transplant one agent's
// native state into another, so it renders the source session's transcript and
// launches the target agent with that transcript as its opening prompt. The
// same-agent case is rejected because the native fork is strictly better there.
func forkInto(ctx context.Context, src session.Source, cmd Command, stdin io.Reader, stdout, stderr io.Writer) error {
	if cmd.Into == src.Ref.Provider {
		return fmt.Errorf("--into %s: the session is already %s's; use catchup fork %s for a native fork with full state", cmd.Into, cmd.Into, cmd.Into)
	}
	if _, err := selectProvider(cmd.Into); err != nil {
		return err
	}
	prov, err := selectProvider(src.Ref.Provider)
	if err != nil {
		return err
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
	var buf bytes.Buffer
	if err := render.Thread(&buf, thread, session.FormatMarkdown); err != nil {
		return err
	}
	prompt := fmt.Sprintf("Continue the work from this prior %s session in this directory. Its transcript follows; pick up where it left off.\n\n%s",
		src.Ref.Provider, buf.String())
	name, args, err := intoCommand(cmd.Into, prompt)
	if err != nil {
		return err
	}
	return runInto(ctx, name, args, stdin, stdout, stderr)
}

// intoCommand maps a target agent to its "start interactive with an opening
// prompt" invocation, the seeding counterpart of forkCommand's native resumes.
func intoCommand(target, prompt string) (string, []string, error) {
	switch target {
	case session.ProviderCodex:
		return "codex", []string{prompt}, nil
	case session.ProviderClaude:
		return "claude", []string{prompt}, nil
	case session.ProviderOpenCode:
		return "opencode", []string{"--prompt", prompt}, nil
	case session.ProviderPiAgent:
		return "pi", []string{prompt}, nil
	default:
		return "", nil, fmt.Errorf("--into: unsupported agent %q", target)
	}
}

func forkCommand(src session.Source) (string, []string, error) {
	switch src.Ref.Provider {
	case session.ProviderCodex:
		if src.Ref.SessionID == "" {
			return "", nil, fmt.Errorf("fork codex: missing session id")
		}
		return "codex", []string{"fork", src.Ref.SessionID}, nil
	case session.ProviderClaude:
		if src.Ref.SessionID == "" {
			return "", nil, fmt.Errorf("fork claude: missing session id")
		}
		return "claude", []string{"--resume", src.Ref.SessionID, "--fork-session"}, nil
	case session.ProviderOpenCode:
		if src.Ref.SessionID == "" {
			return "", nil, fmt.Errorf("fork opencode: missing session id")
		}
		return "opencode", []string{"--session", src.Ref.SessionID, "--fork"}, nil
	case session.ProviderPiAgent:
		target := src.Ref.SessionID
		if src.Path != "" {
			target = src.Path
		}
		if target == "" {
			return "", nil, fmt.Errorf("fork pi-agent: missing session id or path")
		}
		return "pi", []string{"--fork", target}, nil
	default:
		return "", nil, fmt.Errorf("fork: unsupported agent %q", src.Ref.Provider)
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
