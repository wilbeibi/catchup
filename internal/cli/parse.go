package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/wilbeibi/catchup/internal/session"
)

// DefaultLimit is the number of rows a listing returns when -n is not given.
const DefaultLimit = session.DefaultListLimit

// Command is the fully parsed, normalized form of a single catchup invocation:
// what to select (Target) and how to present it. It is the parser's only
// output and the cli's only input, so that argument syntax lives in exactly one
// place and the rest of the program works with structured data.
type Command struct {
	Action       string // optional action subcommand; empty means render history
	Into         string // --into <agent>: with fork, seed that agent with the transcript
	Model        string // --model <name>: with fork, launch the agent with this model
	From         string // --from <file|-|http(s) url>: with fork --into, seed from this artifact instead of a provider store
	Dir          string // --dir <path>: select sessions from this directory instead of the cwd
	Target       session.Target
	Format       session.Format
	MetaOnly     bool // -i: render metadata/frontmatter only
	Full         bool // --full: render oversized entries whole instead of clamped
	LastN        int  // --last N: keep only the last N exchanges/turns (0 = all)
	SinceCompact bool // --since-compact: keep only the final compaction segment
	List         bool // --list: print the ranked listing and exit
	Limit        int  // -n N: cap listing rows (defaults to DefaultLimit)
	Help         bool // --help, -h: print usage and exit
	Version      bool // --version: print the binary version and exit
}

// Parse turns raw argv (excluding the program name) into a Command. It accepts
// the fixed grammar:
//
//	catchup [agent[/<rank>]] [flags]
//
// The agent may be omitted; the cli then detects the agent with the newest
// session in the working directory (Target.Provider stays empty here).
//
// Flags may appear before or after the target, which is why this is a small
// hand-rolled parser rather than the stdlib flag package (which stops at the
// first non-flag argument). Parse is purely syntactic plus a normalization
// pass; it does not check whether the provider actually exists — that belongs
// to the cli dispatch (selectProvider).
func Parse(args []string) (Command, error) {
	cmd := Command{Format: session.FormatMarkdown, Limit: DefaultLimit}

	if len(args) > 0 && (args[0] == "fork" || args[0] == "install-skill") {
		cmd.Action = args[0]
		args = args[1:]
	}

	var (
		target    string
		haveTgt   bool
		formatSet bool
		limitSet  bool
	)

	for i := 0; i < len(args); i++ {
		tok := args[i]

		// Support --flag=value as well as --flag value.
		name, inline, hasInline := tok, "", false
		if strings.HasPrefix(tok, "--") {
			if eq := strings.IndexByte(tok, '='); eq >= 0 {
				name, inline, hasInline = tok[:eq], tok[eq+1:], true
			}
		}
		// value consumes the argument for a value-taking flag, from either the
		// inline =form or the following token.
		value := func() (string, error) {
			if hasInline {
				return inline, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s needs a value", name)
			}
			i++
			return args[i], nil
		}

		switch name {
		case "--html":
			if err := setFormat(&cmd, &formatSet, session.FormatHTML); err != nil {
				return cmd, err
			}
		case "--json":
			if err := setFormat(&cmd, &formatSet, session.FormatJSON); err != nil {
				return cmd, err
			}
		case "--md", "--markdown":
			if err := setFormat(&cmd, &formatSet, session.FormatMarkdown); err != nil {
				return cmd, err
			}
		case "-i", "--info":
			cmd.MetaOnly = true
		case "--list":
			cmd.List = true
		case "-q", "--query":
			v, err := value()
			if err != nil {
				return cmd, err
			}
			cmd.Target.Query = v
		case "--id":
			v, err := value()
			if err != nil {
				return cmd, err
			}
			cmd.Target.SessionID = v
		case "--into":
			v, err := value()
			if err != nil {
				return cmd, err
			}
			cmd.Into = v
		case "--model":
			v, err := value()
			if err != nil {
				return cmd, err
			}
			cmd.Model = v
		case "--from":
			v, err := value()
			if err != nil {
				return cmd, err
			}
			// Rejected here, not in normalize: an empty From reads as
			// "flag absent" there, and the invocation would silently
			// degrade into a plain fork.
			if v == "" {
				return cmd, errors.New("--from needs a value: a file path, - (stdin), or an http(s) URL")
			}
			cmd.From = v
		case "-n", "--limit":
			v, err := value()
			if err != nil {
				return cmd, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return cmd, fmt.Errorf("-n needs a positive integer, got %q", v)
			}
			cmd.Limit, limitSet = n, true
		case "--last":
			v, err := value()
			if err != nil {
				return cmd, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return cmd, fmt.Errorf("--last needs a positive integer, got %q", v)
			}
			cmd.LastN = n
		case "--since-compact":
			cmd.SinceCompact = true
		case "--full":
			cmd.Full = true
		case "--dir":
			v, err := value()
			if err != nil {
				return cmd, err
			}
			cmd.Dir = v
		case "-h", "--help":
			cmd.Help = true
		case "--version":
			cmd.Version = true
		default:
			if strings.HasPrefix(tok, "-") && tok != "-" {
				return cmd, fmt.Errorf("unknown flag %q", tok)
			}
			if haveTgt {
				if looksLikeSessionID(tok) {
					return cmd, fmt.Errorf("unexpected extra argument %q; to select by session id use --id %s", tok, tok)
				}
				return cmd, fmt.Errorf("unexpected extra argument %q (only one agent target is allowed)", tok)
			}
			target, haveTgt = tok, true
		}
	}

	if cmd.Help || cmd.Version {
		return cmd, nil // skip target validation; neither output involves a provider
	}
	if haveTgt {
		if err := applyTarget(&cmd, target); err != nil {
			return cmd, err
		}
	}
	if err := normalize(&cmd); err != nil {
		return cmd, err
	}
	// Checked after normalize so that -q's implicit list mode counts as a
	// listing. Outside a listing -n would be silently ignored, and a silent
	// no-op is worse than an error — especially since -n is the flag people
	// guess when they mean --last.
	if limitSet && !cmd.List {
		return cmd, errors.New("-n only applies to listings; did you mean --last?")
	}
	return cmd, nil
}

// looksLikeSessionID reports whether a stray argument is plausibly a session id
// rather than a mistyped agent name, so the error can point at --id. Ids from
// every provider are long and carry digits (UUIDs, ULID-ish stamps); agent
// names are short and alphabetic.
func looksLikeSessionID(s string) bool {
	if len(s) < 8 {
		return false
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func setFormat(cmd *Command, set *bool, f session.Format) error {
	if *set && cmd.Format != f {
		return errors.New("conflicting output formats; choose one of --md/--html/--json")
	}
	cmd.Format, *set = f, true
	return nil
}

// applyTarget splits "<agent>[/<rank>]" and rejects every other URI shape:
// schemes (agents://...), path segments, and non-numeric ranks. Forbidding a
// non-numeric rank is what guarantees a session id can never be mistaken for a
// rank — ids only ever enter through --id.
func applyTarget(cmd *Command, spec string) error {
	if strings.Contains(spec, "://") || strings.HasPrefix(spec, "agents:") {
		return fmt.Errorf("%q: the agents:// scheme is not supported; use catchup <agent>[/<rank>]", spec)
	}

	provider, rest, hasRank := strings.Cut(spec, "/")
	if provider == "" {
		return errors.New("missing agent name")
	}
	if !isProviderName(provider) {
		return fmt.Errorf("%q: agent name may only contain letters, digits, '-' and '_'", provider)
	}
	cmd.Target.Provider = provider

	if !hasRank {
		return nil
	}
	if rest == "" || strings.Contains(rest, "/") {
		return fmt.Errorf("%q: expected <agent>/<rank> with a single numeric rank", spec)
	}
	rank, err := strconv.Atoi(rest)
	if err != nil || rank < 1 {
		return fmt.Errorf("%q: rank must be a positive integer; use --id for a session id", spec)
	}
	cmd.Target.Rank = rank
	return nil
}

// looksLikeRemoteDir reports whether a --dir value reads as scp syntax
// (host:path). A colon before any path anchor is far more likely a
// transcription of ssh habits than a real directory name; anchor the path
// (/, ./, ~/) to use a local directory that genuinely contains ':'. WHERE
// and SCOPE are separate axes — a directory flag never names a machine.
func looksLikeRemoteDir(dir string) bool {
	i := strings.IndexByte(dir, ':')
	if i <= 0 {
		return false
	}
	switch dir[0] {
	case '/', '.', '~':
		return false
	}
	return true
}

// validateFromSpelling enforces D6's closed set of --from shapes: a file
// path, - (stdin), or a plain http(s) URL. Every other shape is a transport,
// and transports live in the shell — so each rejection names the pipe (or
// presigned URL) that replaces it.
func validateFromSpelling(from string) error {
	if from == "-" || isHTTPURL(from) {
		return nil
	}
	if scheme, _, ok := strings.Cut(from, "://"); ok {
		if scheme == "s3" {
			return errors.New("--from takes a file, - (stdin), or an http(s) URL; presign the object (aws s3 presign) or pipe it: aws s3 cp <s3-url> - | catchup fork --into <agent> --from -")
		}
		return fmt.Errorf("--from takes a file, - (stdin), or an http(s) URL, not %s://; pipe the fetch instead: <fetch it> | catchup fork --into <agent> --from -", scheme)
	}
	if looksLikeRemoteDir(from) {
		return errors.New("--from is local; for another machine's file, pipe it: ssh <host> cat <path> | catchup fork --into <agent> --from -")
	}
	return nil
}

// isHTTPURL reports whether a --from value is the URL spelling. Only plain
// http(s) counts: every other scheme is rejected by validateFromSpelling,
// because the reach of "anything" comes from stdin and http, never from
// per-service integrations.
func isHTTPURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// isProviderName reports whether s is a syntactically valid provider name. This
// is what rejects URI debris (query strings, paths, schemes) at the grammar
// layer; whether the name maps to a real provider is checked later by dispatch.
func isProviderName(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return s != ""
}

// normalize rejects contradictory selectors and applies the one implicit rule:
// a query with no explicit selector means list mode.
func normalize(cmd *Command) error {
	t := cmd.Target
	if cmd.Into != "" && cmd.Action != "fork" {
		return errors.New("--into only applies to fork")
	}
	if cmd.Model != "" && cmd.Action != "fork" {
		return errors.New("--model only applies to fork; it names the launched agent's model")
	}
	if cmd.Action == "install-skill" {
		if cmd.List || cmd.MetaOnly || cmd.Full || cmd.LastN > 0 || cmd.SinceCompact ||
			cmd.Dir != "" || cmd.From != "" || t.Query != "" || t.Rank > 0 || t.SessionID != "" {
			return errors.New("install-skill takes only an agent name")
		}
	}
	if cmd.Action == "fork" {
		// fork launches an agent, so the render views make no sense on it.
		// The selectors (-q, /rank, --id) pick the fork source exactly as
		// they pick a read; the trims and --full apply only with --into,
		// where they shape the seeded transcript.
		intoFork := cmd.Into != ""
		switch {
		case cmd.List:
			return errors.New("fork cannot be combined with --list; fork -q lists the matches when several fit")
		case cmd.MetaOnly:
			return errors.New("fork cannot be combined with -i")
		case cmd.LastN > 0 && !intoFork:
			return errors.New("fork cannot be combined with --last (with --into it bounds the seeded transcript)")
		case cmd.SinceCompact && !intoFork:
			return errors.New("fork cannot be combined with --since-compact (with --into it bounds the seeded transcript)")
		case cmd.Full && !intoFork:
			return errors.New("fork cannot be combined with --full (with --into it seeds the unclamped transcript)")
		}
	}
	// --from replaces the provider store as the session source (D6): the
	// artifact is the selection, so the store selectors can never apply; and
	// until catchup parses artifacts back into a Thread, neither can the
	// trims — those errors navigate to trimming at render time instead.
	if cmd.From != "" {
		switch {
		case cmd.Action != "fork":
			return errors.New("--from only applies to fork --into; the artifact is already the rendered form — open it directly to read it")
		case cmd.Into == "":
			return errors.New("--from needs --into <agent>: an artifact carries no native state to resume")
		case t.Provider != "" || t.Query != "" || t.Rank > 0 || t.SessionID != "":
			return errors.New("--from is the session source; an agent name, -q, /rank, and --id select from a provider store and do not apply")
		case cmd.Dir != "":
			return errors.New("--from reads an artifact; --dir scopes provider stores and does not apply")
		case cmd.LastN > 0 || cmd.SinceCompact:
			return errors.New("--last/--since-compact do not apply to --from (the artifact is seeded verbatim); trim when rendering: catchup <agent> --last 20 > s.md")
		case cmd.Full:
			return errors.New("--full does not apply to --from; the artifact was clamped (or not) when it was rendered")
		}
		if err := validateFromSpelling(cmd.From); err != nil {
			return err
		}
	}
	switch {
	case t.SessionID != "" && t.Provider == "":
		return errors.New("--id needs an agent name; id formats are per-agent")
	case t.SessionID != "" && t.Rank > 0:
		return errors.New("--id cannot be combined with a /rank selector")
	case t.SessionID != "" && cmd.List:
		return errors.New("--id cannot be combined with --list")
	case t.SessionID != "" && t.Query != "":
		return errors.New("--id cannot be combined with -q")
	case t.SessionID != "" && cmd.Dir != "":
		return errors.New("--id selects one exact session; --dir does not apply")
	case looksLikeRemoteDir(cmd.Dir):
		return errors.New("--dir is a local directory; to read another machine's sessions run catchup there: ssh box catchup <agent> --dir <path>")
	case t.Rank > 0 && cmd.List:
		return errors.New("a /rank selector cannot be combined with --list")
	case cmd.MetaOnly && cmd.List:
		return errors.New("-i cannot be combined with --list")
	case cmd.LastN > 0 && cmd.SinceCompact:
		return errors.New("--last cannot be combined with --since-compact; they are alternative trims")
	case cmd.Full && cmd.Format == session.FormatJSON:
		return errors.New("--json is never clamped; --full only applies to --md/--html")
	case cmd.Full && cmd.MetaOnly:
		return errors.New("--full cannot be combined with -i; -i shows no message bodies")
	}

	// -q implies list mode unless a concrete row was selected by rank or id —
	// or the action is fork, where a bare query picks the fork source instead.
	if cmd.Action == "" && t.Query != "" && t.Rank == 0 && t.SessionID == "" {
		cmd.List = true
	}
	// Checked after the implicit rule so -q listings are covered too. There
	// is no HTML listing view; ignoring the flag would be a silent no-op.
	if cmd.List && cmd.Format == session.FormatHTML {
		return errors.New("--html does not apply to listings; use --json or the default table")
	}
	if cmd.List && cmd.Full {
		return errors.New("--full does not apply to listings; message bodies are not shown")
	}
	return nil
}
