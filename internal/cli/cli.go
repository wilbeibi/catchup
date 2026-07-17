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
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wilbeibi/catchup/internal/agy"
	"github.com/wilbeibi/catchup/internal/claude"
	"github.com/wilbeibi/catchup/internal/cline"
	"github.com/wilbeibi/catchup/internal/codex"
	"github.com/wilbeibi/catchup/internal/cursor"
	"github.com/wilbeibi/catchup/internal/kimi"
	"github.com/wilbeibi/catchup/internal/opencode"
	"github.com/wilbeibi/catchup/internal/piagent"
	"github.com/wilbeibi/catchup/internal/render"
	"github.com/wilbeibi/catchup/internal/session"
)

const helpText = `Usage: catchup [agent[/<rank>]] [flags]        read a past session
       catchup fork [agent] [--into <agent>]   continue one
       catchup fork --into <agent> --from <file | - | url>
       catchup install-skill [agent]

Agents: codex, claude, agy (Antigravity), cline, cursor, kimi, opencode, pi-agent
Omit the agent to use whichever has the newest session here. Bare ` + "`catchup`" + `
prints that session in full, as Markdown. The flags refine three things:
which session, how much of it, and as what.

RECAP — how much of the session (default: all of it)
  --since-compact     just the tail after the last compaction
  --last <N>          just the last N exchanges
  --full              oversized messages whole, not clamped
  -i, --info          metadata only, no messages

FIND — which session (default: newest here)
  --list              list recent sessions
  -q, --query <text>  search by keyword (implies --list)
  <agent>/<rank>      the Nth newest, e.g. codex/3
  --id <id>           an exact session id
  --dir <path>        sessions from another directory, not the cwd
  -n, --limit <N>     cap the listing (default 20)

HAND OFF — continue the work
  fork [agent]        native resume, full state
  --into <agent>      seed a different agent with the transcript
  --from <src>        with --into: seed from a file, - (stdin), or http(s) URL
  --model <name>      launch it on a specific model, e.g. a cheaper one
                      (the target agent's own model name, e.g. gpt-5.6)

OUTPUT — as what (default: Markdown)
  --md, --markdown    Markdown (the default)
  --json              JSON, for scripting (never clamped)
  --html              a self-contained page, for sharing

Meta: --version (print version) · -h, --help (this text)

Examples:
  catchup claude --since-compact       recover a Claude session after compaction
  catchup codex -q "deploy"            find the Codex session about deploys
  catchup codex/3                      read the 3rd most recent Codex session
  catchup claude --dir ~/src/proj      latest session from another directory
  catchup fork --into codex            hand the newest session here to Codex
  catchup fork claude --into codex --model gpt-5.6   ...on a specific model
  catchup fork --into claude --from handoff.md       ...from a saved transcript
  catchup install-skill                install the SKILL.md for every agent

Recipes — stdout is the wire format, so pipes and files are the transport:
  catchup claude | rg -C3 "deploy"      search inside a session
  catchup claude --html > session.html  share a session as a page
  ssh box catchup codex --last 20       read a session from another machine
  catchup codex > handoff.md            a file travels by anything — scp,
                                        wormhole, S3, Dropbox, a paste
  catchup fork --into claude --from handoff.md
                                        ...and this is the receiving end: seed
                                        any agent from a file, URL, or stdin
  ssh box catchup codex | catchup fork --into claude --from -
                                        another machine's session, no file
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
		if cmd.From != "" {
			return forkFrom(ctx, cmd, stdin, stdout, stderr)
		}
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
		session.ProviderCline,
		session.ProviderCursor,
		session.ProviderKimi,
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
	case session.ProviderCline:
		return cline.New(), nil
	case session.ProviderCursor:
		return cursor.New(), nil
	case session.ProviderKimi:
		return kimi.New(), nil
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
		return nil, fmt.Errorf("unknown agent %q (want codex, claude, agy, cline, cursor, kimi, opencode, or pi-agent); run catchup --help", name)
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
	// Kimi refuses to resume a session from any directory but the one it was
	// created in (verified against kimi-code 0.26), so catching the mismatch
	// here gives a plain answer instead of kimi's failed-launch error.
	if src.Ref.Provider == session.ProviderKimi {
		if cwd, err := os.Getwd(); err == nil {
			if home := src.Metadata["cwd"]; home != "" && home != cwd {
				return fmt.Errorf("fork kimi: kimi only resumes a session inside its own directory; run: cd %q && catchup fork kimi --id %s, or seed another agent here with --into", home, src.Ref.SessionID)
			}
		}
	}
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
	prompt := fmt.Sprintf("Continue the work from this prior %s session in this directory. Its transcript follows; pick up where it left off.\n\n%s",
		src.Ref.Provider, buf.String())
	return seedInto(ctx, cmd.Into, cmd.Model, prompt,
		"rerun with --last 20 or --since-compact to trim what gets seeded", stdin, stdout, stderr)
}

// openTTY reopens the controlling terminal. It becomes the launched agent's
// stdin when --from - consumed the pipe; a var so tests can stand in a fake
// terminal.
var openTTY = func() (io.ReadCloser, error) { return os.Open("/dev/tty") }

// forkFrom is the artifact half of fork (D6 level 1): it seeds the --into
// agent from a document that did not come from a provider store — a file,
// stdin, or a plain-GET URL. The artifact is seeded verbatim (any text
// document works, not only catchup output), so no Thread is built and the
// trims cannot apply; parse.go rejects those combinations. Same-agent --into
// is allowed by construction: no native state stands behind an artifact, so
// the seed is the best continuation there is.
func forkFrom(ctx context.Context, cmd Command, stdin io.Reader, stdout, stderr io.Writer) error {
	if _, err := selectProvider(cmd.Into); err != nil {
		return err
	}
	label := fromLabel(cmd.From)
	body, err := readArtifact(ctx, cmd.From, stdin)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("--from %s: the artifact is empty; nothing to seed", label)
	}
	prompt := fmt.Sprintf("Continue the work described in this document — a prior agent session's transcript or handoff notes. Pick up where it left off.\n\nSource: %s\n\n%s",
		label, body)
	// --from - consumed the pipe, so the launched agent gets the controlling
	// terminal instead of an exhausted reader (the fzf / git-am pattern).
	if cmd.From == "-" {
		tty, err := openTTY()
		if err != nil {
			return errors.New("--from -: stdin fed the artifact and no terminal is left for the agent; save it to a file and use --from <path>")
		}
		defer tty.Close()
		stdin = tty
	}
	return seedInto(ctx, cmd.Into, cmd.Model, prompt,
		"trim when rendering the artifact: catchup <agent> --last 20 > s.md", stdin, stdout, stderr)
}

// fromLabel renders a --from value for prompts and error text: stdin gets a
// name, and a URL drops its query string — presigned links carry their auth
// there, and the label persists in the seeded agent's own session store.
func fromLabel(from string) string {
	switch {
	case from == "-":
		return "stdin"
	case isHTTPURL(from):
		if u, err := url.Parse(from); err == nil && u.RawQuery != "" {
			u.RawQuery = ""
			return u.String()
		}
	}
	return from
}

// readArtifact fetches the --from artifact's bytes. The spelling was
// validated at parse time; the three shapes just dispatch here.
func readArtifact(ctx context.Context, from string, stdin io.Reader) ([]byte, error) {
	switch {
	case from == "-":
		if stdin == nil {
			return nil, errors.New("--from -: stdin is not available")
		}
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("--from -: %w", err)
		}
		return b, nil
	case isHTTPURL(from):
		return fetchArtifact(ctx, from)
	default:
		b, err := os.ReadFile(expandTilde(from))
		if err != nil {
			return nil, fmt.Errorf("--from: %w", err)
		}
		return b, nil
	}
}

// artifactClient fetches --from URLs: one minute end-to-end, because a
// stalled GET in a scripted invocation would otherwise hang forever —
// ctrl-C is not standing by everywhere the socket is used.
var artifactClient = &http.Client{Timeout: time.Minute}

// fetchArtifact GETs an http(s) artifact: redirects followed (presigned and
// share links use them), auth only ever inside the URL, no size cap — the
// seedInto gates price the seed instead. Errors carry the query-stripped
// label, so presigned auth never lands in error text.
func fetchArtifact(ctx context.Context, rawURL string) ([]byte, error) {
	label := fromLabel(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("--from %s: %w", label, err)
	}
	resp, err := artifactClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("--from %s: %w", label, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("--from %s: %s", label, resp.Status)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("--from %s: %w", label, err)
	}
	return b, nil
}

// warnTranscriptBytes is the prompt size above which seedInto warns:
// ~32k tokens at the usual ~4 bytes per token, roughly where a seeded
// transcript starts crowding a target agent's working context.
const warnTranscriptBytes = 128 * 1024

// seedInto starts the --into agent with a transcript as its opening prompt —
// the launch half shared by forkInto and forkFrom. trimHint names the
// oversize recovery in the caller's own grammar, because the two sources
// trim differently: a store read re-runs with --last/--since-compact, an
// artifact is re-rendered at its source. The warning goes to stderr so the
// seeded prompt stays clean.
func seedInto(ctx context.Context, into, model, prompt, trimHint string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(prompt) > warnTranscriptBytes {
		fmt.Fprintf(stderr, "catchup: transcript is large (~%dk tokens); %s\n", len(prompt)/4/1000, trimHint)
	}
	name, args, err := intoCommand(into, prompt, model)
	if err != nil {
		return err
	}
	err = runInto(ctx, name, args, stdin, stdout, stderr)
	// The OS, not catchup, owns the argv ceiling (Linux caps one exec
	// argument at 128 KB; other platforms are roomier), so no size is
	// pre-rejected here — but exec's own refusal is terse, so translate
	// it into the trim navigation.
	if errors.Is(err, syscall.E2BIG) {
		return fmt.Errorf("cannot launch %s: the %d KB seed prompt exceeds this OS's argument limit; %s", into, len(prompt)/1024, trimHint)
	}
	return err
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
	case session.ProviderCline:
		// -i opens the TUI, which auto-submits a positional initial prompt.
		// Without it a bare prompt runs headless and exits on completion.
		return "cline", append(modelArgs("--model", model), "-i", prompt), nil
	case session.ProviderCursor:
		return "cursor-agent", append(modelArgs("--model", model), prompt), nil
	case session.ProviderKimi:
		// Kimi rejects positional arguments and its -p flag is
		// non-interactive print mode, so there is no way to start an
		// interactive session with a seed prompt (checked against
		// kimi-code v0.26.0). Refusing beats launching a headless run
		// that answers once and exits.
		return "", nil, fmt.Errorf("--into kimi: kimi cannot start interactive with a seed prompt; fork kimi resumes a kimi session natively")
	default:
		return "", nil, fmt.Errorf("--into: unsupported agent %q", target)
	}
}

// modelArgs renders a model selection as the target agent's flag pair, or
// nothing when no model was requested. The value is passed verbatim: it
// must be the launched agent's own model name.
//
// The flag spellings forkCommand and intoCommand emit are foreign CLIs'
// surface and can drift; all eight were last checked against the installed
// CLIs' --help on 2026-07-17 (codex takes -m; agy, claude, opencode, pi,
// kimi, cline, and cursor-agent take --model). Kimi's short flags churn
// across releases (-C became -c in 0.26; -r is a hidden alias of -S), so its
// case emits long forms only.
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
	case session.ProviderKimi:
		if src.Ref.SessionID == "" {
			return "", nil, fmt.Errorf("fork kimi: missing session id")
		}
		// Kimi has no fork; --session is its native resume.
		return "kimi", append([]string{"--session", src.Ref.SessionID}, modelArgs("--model", model)...), nil
	case session.ProviderCline:
		if src.Ref.SessionID == "" {
			return "", nil, fmt.Errorf("fork cline: missing session id")
		}
		// Cline has no fork, and a bare --id only prints a session summary
		// and exits; -i opens the TUI resumed on the session.
		return "cline", append([]string{"-i", "--id", src.Ref.SessionID}, modelArgs("--model", model)...), nil
	case session.ProviderCursor:
		if src.Ref.SessionID == "" {
			return "", nil, fmt.Errorf("fork cursor: missing session id")
		}
		// Cursor has no fork; --resume is its native resume.
		return "cursor-agent", append([]string{"--resume", src.Ref.SessionID}, modelArgs("--model", model)...), nil
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
