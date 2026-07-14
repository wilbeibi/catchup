// Package cli is the wiring layer. It parses argv into a Command, dispatches to
// the selected Provider, and hands the result to the renderer. It owns no
// formatting and no history-reading logic of its own — it only sequences the
// other layers. The pipeline reads top to bottom: parse, locate, read, render.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wilbeibi/catchup/internal/agy"
	"github.com/wilbeibi/catchup/internal/claude"
	"github.com/wilbeibi/catchup/internal/codex"
	"github.com/wilbeibi/catchup/internal/opencode"
	"github.com/wilbeibi/catchup/internal/piagent"
	"github.com/wilbeibi/catchup/internal/render"
	"github.com/wilbeibi/catchup/internal/session"
)

const helpText = `Usage: catchup [agent[/<rank>]] [flags]
       catchup fork [agent[/<rank>]] [--into <agent>] [--model <name>]
       catchup install-skill [agent]

Agents: codex, claude, agy (Antigravity), opencode, pi-agent
Omit the agent to use whichever one has the newest session in this directory.

Flags:
  --list              list recent sessions
  -q, --query <text>  filter by keyword (implies --list)
  --id <id>           select by exact session id
  -i, --info          print metadata only, no messages
  --last <N>          show last N exchanges only
  --since-compact     show only the final compaction segment
  --dir <path>        select sessions from this directory instead of the cwd
  --full              show oversized messages whole instead of clamped
  --json              output JSON (never clamped)
  --html              output HTML
  --md, --markdown    output Markdown (default)
  -n, --limit <N>     cap listing rows (default 20)
  --into <agent>      with fork: start a different agent, seeded with the transcript
  --model <name>      with fork: launch the agent with this model (use the
                      launched agent's own model name, e.g. gpt-5.6)
  --version           print the catchup version
  -h, --help          print this help

Examples:
  catchup                     latest session from any agent → Markdown
  catchup claude              latest Claude session → Markdown
  catchup claude --list       list recent Claude sessions
  catchup codex -q "deploy"   search Codex sessions by keyword
  catchup codex/3             3rd most recent Codex session
  catchup claude --last 5     last 5 exchanges
  catchup claude --since-compact  tail after last compaction
  catchup claude --dir ~/src/proj  latest session from another directory
  catchup fork                fork the latest session in this directory
  catchup fork codex          fork the latest Codex session in this directory
  catchup fork codex/3        fork the 3rd most recent Codex session
  catchup fork codex -q "auth"  fork the Codex session matching a keyword
  catchup fork codex --into claude  continue the Codex session in Claude
  catchup fork claude --into codex --model gpt-5.6  ...on a specific model
  catchup install-skill       install catchup's SKILL.md for every detected agent
  catchup install-skill codex install catchup's SKILL.md for Codex only

Recipes — stdout is the wire format, so pipes and files are the transport:
  catchup claude | rg -C3 "deploy"      search inside a session
  catchup claude --html > session.html  share a session as a page
  ssh box catchup codex --last 20       read a session from another machine
  catchup codex > handoff.md            a file travels by anything — scp,
                                        wormhole, S3, Dropbox, a paste
  git worktree add ../fix && cd ../fix && catchup fork claude --dir ~/src/proj
                                        continue a session without two agents
                                        editing one tree
`

type forkRunner func(context.Context, session.Source, string, io.Reader, io.Writer, io.Writer) error

var runFork forkRunner = execFork

// Run executes one invocation. current maps a provider name to the id of the
// session we are running inside, when that agent injects one (see
// session.ResolveCurrent); it lets the default selection target the live
// session exactly rather than guessing by recency. skillDirs maps a provider
// name to its global Agent Skills directory (see session.ResolveSkillDirs)
// and skillMD is the SKILL.md content to install there; install-skill writes
// them, every other action reads skillDirs to warn when an installed copy
// drifts from this build. version is the stamped build version --version
// reports.
func Run(ctx context.Context, args []string, roots session.Roots, current map[string]string, skillDirs map[string]string, skillMD []byte, version, cwd string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd, err := Parse(args)
	if err != nil {
		return err
	}

	if cmd.Help {
		fmt.Fprint(stdout, helpText)
		return nil
	}
	if cmd.Version {
		fmt.Fprintln(stdout, "catchup", version)
		return nil
	}

	// Installed skill copies ship separately from the binary; surface version
	// drift before doing anything else. install-skill itself is the fix.
	if cmd.Action != "install-skill" {
		warnSkillDrift(skillDirs, version, stderr)
	}

	// --dir overrides the directory that scopes every selection below —
	// reads, listings, and fork sources alike. It also outranks an injected
	// current-session id: pointing at another directory means pointing away
	// from the session this process is sitting in.
	if cmd.Dir != "" {
		cwd = expandTilde(cmd.Dir)
		current = nil
	}

	if cmd.Action == "fork" {
		src, ok, err := locateForkSource(ctx, roots, cmd, cwd, stdout, stderr)
		if err != nil {
			return err
		}
		if !ok {
			return nil // several query matches: the listing printed is the answer
		}
		if cmd.Into != "" {
			return forkInto(ctx, src, cmd, stdin, stdout, stderr)
		}
		return runFork(ctx, src, cmd.Model, stdin, stdout, stderr)
	}

	if cmd.Action == "install-skill" {
		return installSkill(cmd.Target.Provider, skillDirs, skillMD, version, stdout)
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
		return render.List(stdout, cmd.Target.Provider, summaries, cmd.Format)
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
	if !cmd.Full && cmd.Format != session.FormatJSON {
		thread = clampEntries(thread)
	}
	return render.Thread(stdout, thread, cmd.Format)
}

// expandTilde resolves a leading ~ against this process's home. The shell
// normally does this, but --dir=~/x (the inline = form) and values quoted in
// scripts arrive with the tilde intact.
func expandTilde(dir string) string {
	if dir != "~" && !strings.HasPrefix(dir, "~/") {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return dir
	}
	return filepath.Join(home, strings.TrimPrefix(dir, "~"))
}

func providerNames() []string {
	return []string{
		session.ProviderCodex,
		session.ProviderClaude,
		session.ProviderAgy,
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
	case session.ProviderAgy:
		return agy.New(), nil
	case session.ProviderOpenCode:
		return opencode.New(), nil
	case session.ProviderPiAgent:
		return piagent.New(), nil
	default:
		if name == "list" {
			return nil, fmt.Errorf(`unknown agent "list"; did you mean catchup --list?`)
		}
		if name == "version" {
			return nil, fmt.Errorf(`unknown agent "version"; did you mean catchup --version?`)
		}
		if name == "antigravity" {
			return nil, fmt.Errorf(`unknown agent "antigravity"; Antigravity's agent name is agy`)
		}
		return nil, fmt.Errorf("unknown agent %q (want codex, claude, agy, opencode, or pi-agent); run catchup --help", name)
	}
}

// locateForkSource picks the session a fork continues. Selectors compose
// exactly as they do for a read — --id resolves the exact session, /rank
// indexes the (optionally query-filtered) cwd listing, and the default is
// the newest session in cwd — with one fork-specific rule: a bare query
// forks only on a unique hit. On several hits it prints the listing and
// returns ok=false without an error; the listing plus a rerun hint is the
// answer, matching how -q behaves on a read.
func locateForkSource(ctx context.Context, roots session.Roots, cmd Command, cwd string, stdout, stderr io.Writer) (session.Source, bool, error) {
	if cmd.Target.Provider == "" {
		src, err := newestAcross(ctx, roots, cwd)
		if err != nil {
			return session.Source{}, false, fmt.Errorf("fork: %w", err)
		}
		t := cmd.Target
		if t.Query == "" && t.Rank == 0 && t.SessionID == "" {
			return src, true, nil
		}
		// A selector re-selects within the detected agent, same as a read.
		cmd.Target.Provider = src.Ref.Provider
	}
	prov, err := selectProvider(cmd.Target.Provider)
	if err != nil {
		return session.Source{}, false, err
	}

	t := cmd.Target
	if t.Query != "" && t.Rank == 0 && t.SessionID == "" {
		sums, err := prov.List(ctx, roots, session.ListOptions{Query: t.Query, Cwd: cwd, Limit: cmd.Limit})
		if err != nil {
			return session.Source{}, false, err
		}
		switch len(sums) {
		case 0:
			return session.Source{}, false, fmt.Errorf("fork %s: no sessions matching %q in %s", t.Provider, t.Query, cwd)
		case 1:
			src, err := prov.Resolve(ctx, roots, sums[0].Ref.SessionID)
			if err != nil {
				return session.Source{}, false, err
			}
			return stampProvider(src, t.Provider), true, nil
		default:
			if err := render.List(stdout, t.Provider, sums, cmd.Format); err != nil {
				return session.Source{}, false, err
			}
			fmt.Fprintf(stderr, "catchup: %d sessions match %q; rerun as catchup fork %s/<rank> -q %q\n",
				len(sums), t.Query, t.Provider, t.Query)
			return session.Source{}, false, nil
		}
	}

	src, err := locate(ctx, prov, roots, cmd, cwd, nil)
	if err != nil {
		return session.Source{}, false, err
	}
	return stampProvider(src, t.Provider), true, nil
}

// stampProvider fills a Source's provider name when the provider's Resolve
// left it empty, so downstream dispatch (forkCommand, seed prompts) always
// knows which agent the session belongs to.
func stampProvider(src session.Source, name string) session.Source {
	if src.Ref.Provider == "" {
		src.Ref.Provider = name
	}
	return src
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
		// Every provider struck out; each failure line already carries its
		// own hint (where that agent's sessions actually are, or that it has
		// none), so the aggregate reads as a diagnosis, not a dead end.
		var b strings.Builder
		fmt.Fprintf(&b, "no sessions found in %s", cwd)
		for _, f := range failures {
			b.WriteString("\n  ")
			b.WriteString(f.Error())
		}
		return session.Source{}, errors.New(b.String())
	}
	return session.Source{}, fmt.Errorf("no agents available")
}

// installSkill writes skillMD to "<dir>/catchup/SKILL.md" for one provider, or
// for every provider in providerNames() when provider is "". Providers with no
// entry in skillDirs are skipped rather than erroring, so the closed provider
// set and the skill-directory set can evolve independently. Each copy is
// stamped with the build version so later runs can detect drift.
func installSkill(provider string, skillDirs map[string]string, skillMD []byte, version string, stdout io.Writer) error {
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
		if err := os.WriteFile(path, stampSkillVersion(skillMD, version), 0o644); err != nil {
			return fmt.Errorf("install-skill %s: %w", name, err)
		}
		fmt.Fprintf(stdout, "installed %s\n", path)
	}
	return nil
}

// newestInCwd resolves a provider's newest session in cwd. When there is
// none, the error says where that provider's sessions actually are, so a
// first run in the wrong directory ends with a next step instead of a dead
// end.
func newestInCwd(ctx context.Context, prov session.Provider, roots session.Roots, name, cwd string) (session.Source, error) {
	sums, err := prov.List(ctx, roots, session.ListOptions{Cwd: cwd, Limit: 1})
	if err != nil {
		return session.Source{}, err
	}
	if len(sums) == 0 {
		return session.Source{}, fmt.Errorf("%s: no sessions in %s; %s", name, cwd, absenceHint(ctx, prov, roots))
	}
	src, err := prov.Resolve(ctx, roots, sums[0].Ref.SessionID)
	if err != nil {
		return session.Source{}, err
	}
	if src.Ref.Provider == "" {
		src.Ref.Provider = name
	}
	return src, nil
}

// absenceHint reports where a provider's sessions are when the current
// directory has none: the newest session's directory and age, or the fact
// that the provider has no readable history at all.
func absenceHint(ctx context.Context, prov session.Provider, roots session.Roots) string {
	sums, err := prov.List(ctx, roots, session.ListOptions{Limit: 1})
	if err != nil || len(sums) == 0 {
		return "no sessions anywhere"
	}
	s := sums[0]
	where := s.Cwd
	if where == "" {
		where = "an unknown directory"
	}
	if s.UpdatedAt.IsZero() {
		return fmt.Sprintf("newest is in %s", where)
	}
	return fmt.Sprintf("newest is in %s (%s)", where, s.UpdatedAt.Local().Format("2006-01-02 15:04"))
}

// resolveRank returns the rank-th Source (1-based) of the listing the provider
// would produce for opts. Ranks resolve the same way for every provider — the
// row order List returns — so the composition lives here once instead of
// behind the Provider interface five times.
func resolveRank(ctx context.Context, prov session.Provider, name string, roots session.Roots, opts session.ListOptions, rank int) (session.Source, error) {
	if rank < 1 {
		return session.Source{}, fmt.Errorf("%s: rank must be >= 1", name)
	}
	opts.Limit = rank
	sums, err := prov.List(ctx, roots, opts)
	if err != nil {
		return session.Source{}, err
	}
	if rank > len(sums) {
		return session.Source{}, fmt.Errorf("%s: rank %d out of range (%d matching sessions)", name, rank, len(sums))
	}
	return prov.Resolve(ctx, roots, sums[rank-1].Ref.SessionID)
}

func execFork(ctx context.Context, src session.Source, model string, stdin io.Reader, stdout, stderr io.Writer) error {
	name, args, err := forkCommand(src, model)
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
	if !cmd.Full {
		thread = clampEntries(thread)
	}
	var buf bytes.Buffer
	if err := render.Thread(&buf, thread, session.FormatMarkdown); err != nil {
		return err
	}
	warnLargeTranscript(stderr, buf.Len())
	prompt := fmt.Sprintf("Continue the work from this prior %s session in this directory. Its transcript follows; pick up where it left off.\n\n%s",
		src.Ref.Provider, buf.String())
	name, args, err := intoCommand(cmd.Into, prompt, cmd.Model)
	if err != nil {
		return err
	}
	return runInto(ctx, name, args, stdin, stdout, stderr)
}

// warnTranscriptBytes is the transcript size above which forkInto warns:
// ~32k tokens at the usual ~4 bytes per token, roughly where a seeded
// transcript starts crowding a target agent's working context.
const warnTranscriptBytes = 128 * 1024

// warnLargeTranscript tells the user a seeded transcript is big and, per
// the errors-as-navigation rule, what to do about it. It never blocks the
// launch; the warning goes to stderr so the seeded prompt stays clean.
func warnLargeTranscript(stderr io.Writer, n int) {
	if n <= warnTranscriptBytes {
		return
	}
	fmt.Fprintf(stderr, "catchup: transcript is large (~%dk tokens); consider --last 20 or --since-compact to trim what gets seeded\n", n/4/1000)
}

// intoCommand maps a target agent to its "start interactive with an opening
// prompt" invocation, the seeding counterpart of forkCommand's native
// resumes. A non-empty model becomes the agent's own model flag, placed
// before the positional prompt where the agents that take one require it.
func intoCommand(target, prompt, model string) (string, []string, error) {
	switch target {
	case session.ProviderCodex:
		return "codex", append(modelArgs("-m", model), prompt), nil
	case session.ProviderClaude:
		return "claude", append(modelArgs("--model", model), prompt), nil
	case session.ProviderAgy:
		return "agy", append(modelArgs("--model", model), "-i", prompt), nil
	case session.ProviderOpenCode:
		return "opencode", append(modelArgs("--model", model), "--prompt", prompt), nil
	case session.ProviderPiAgent:
		return "pi", append(modelArgs("--model", model), prompt), nil
	default:
		return "", nil, fmt.Errorf("--into: unsupported agent %q", target)
	}
}

// modelArgs renders a model selection as the target agent's flag pair, or
// nothing when no model was requested. The value is passed verbatim: it
// must be the launched agent's own model name.
//
// The flag spellings forkCommand and intoCommand emit are foreign CLIs'
// surface and can drift; all five were last checked against the installed
// CLIs' --help on 2026-07-10 (codex takes -m; agy, claude, opencode, and pi
// take --model).
func modelArgs(flag, model string) []string {
	if model == "" {
		return nil
	}
	return []string{flag, model}
}

// forkCommand maps a source session to its agent's native resume/fork
// invocation. A non-empty model is appended as the agent's own model flag
// (every supported agent accepts it alongside its resume form).
func forkCommand(src session.Source, model string) (string, []string, error) {
	switch src.Ref.Provider {
	case session.ProviderCodex:
		if src.Ref.SessionID == "" {
			return "", nil, fmt.Errorf("fork codex: missing session id")
		}
		return "codex", append([]string{"fork", src.Ref.SessionID}, modelArgs("-m", model)...), nil
	case session.ProviderClaude:
		if src.Ref.SessionID == "" {
			return "", nil, fmt.Errorf("fork claude: missing session id")
		}
		return "claude", append([]string{"--resume", src.Ref.SessionID, "--fork-session"}, modelArgs("--model", model)...), nil
	case session.ProviderAgy:
		if src.Ref.SessionID == "" {
			return "", nil, fmt.Errorf("fork agy: missing session id")
		}
		// Antigravity has no fork; --conversation is its native resume.
		return "agy", append([]string{"--conversation", src.Ref.SessionID}, modelArgs("--model", model)...), nil
	case session.ProviderOpenCode:
		if src.Ref.SessionID == "" {
			return "", nil, fmt.Errorf("fork opencode: missing session id")
		}
		return "opencode", append([]string{"--session", src.Ref.SessionID, "--fork"}, modelArgs("--model", model)...), nil
	case session.ProviderPiAgent:
		target := src.Ref.SessionID
		if src.Path != "" {
			target = src.Path
		}
		if target == "" {
			return "", nil, fmt.Errorf("fork pi-agent: missing session id or path")
		}
		return "pi", append([]string{"--fork", target}, modelArgs("--model", model)...), nil
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
		opts := session.ListOptions{Query: cmd.Target.Query, Cwd: cwd}
		return resolveRank(ctx, prov, cmd.Target.Provider, roots, opts, cmd.Target.Rank)
	case current[cmd.Target.Provider] != "":
		return prov.Resolve(ctx, roots, current[cmd.Target.Provider])
	default:
		return newestInCwd(ctx, prov, roots, cmd.Target.Provider, cwd)
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
