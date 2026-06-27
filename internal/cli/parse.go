package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/wilbeibi/baton/internal/session"
)

// DefaultLimit is the number of rows a listing returns when -n is not given.
const DefaultLimit = 20

// Command is the fully parsed, normalized form of a single baton invocation:
// what to select (Target) and how to present it. It is the parser's only
// output and the cli's only input, so that argument syntax lives in exactly one
// place and the rest of the program works with structured data.
type Command struct {
	Target       session.Target
	Format       session.Format
	MetaOnly     bool // -I: render metadata/frontmatter only
	LastN        int  // --last N: keep only the last N exchanges/turns (0 = all)
	SinceCompact bool // --since-compact: keep only the final compaction segment
	List         bool // --list: print the ranked listing and exit
	Limit        int  // -n N: cap listing rows (defaults to DefaultLimit)
}

// Parse turns raw argv (excluding the program name) into a Command. It accepts
// the fixed grammar:
//
//	baton <provider>[/<rank>] [flags]
//
// Flags may appear before or after the target, which is why this is a small
// hand-rolled parser rather than the stdlib flag package (which stops at the
// first non-flag argument). Parse is purely syntactic plus a normalization
// pass; it does not check whether the provider actually exists — that belongs
// to the cli dispatch (selectProvider).
func Parse(args []string) (Command, error) {
	cmd := Command{Format: session.FormatMarkdown, Limit: DefaultLimit}

	var (
		target    string
		haveTgt   bool
		formatSet bool
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
		case "-I":
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
		case "-n":
			v, err := value()
			if err != nil {
				return cmd, err
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return cmd, fmt.Errorf("-n needs a positive integer, got %q", v)
			}
			cmd.Limit = n
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
		default:
			if strings.HasPrefix(tok, "-") && tok != "-" {
				return cmd, fmt.Errorf("unknown flag %q", tok)
			}
			if haveTgt {
				return cmd, fmt.Errorf("unexpected extra argument %q (only one provider target is allowed)", tok)
			}
			target, haveTgt = tok, true
		}
	}

	if !haveTgt {
		return cmd, errors.New("missing provider; usage: baton <provider>[/<rank>]")
	}
	if err := applyTarget(&cmd, target); err != nil {
		return cmd, err
	}
	if err := normalize(&cmd); err != nil {
		return cmd, err
	}
	return cmd, nil
}

func setFormat(cmd *Command, set *bool, f session.Format) error {
	if *set && cmd.Format != f {
		return errors.New("conflicting output formats; choose one of --md/--html/--json")
	}
	cmd.Format, *set = f, true
	return nil
}

// applyTarget splits "<provider>[/<rank>]" and rejects every other URI shape:
// schemes (agents://...), path segments, and non-numeric ranks. Forbidding a
// non-numeric rank is what guarantees a session id can never be mistaken for a
// rank — ids only ever enter through --id.
func applyTarget(cmd *Command, spec string) error {
	if strings.Contains(spec, "://") || strings.HasPrefix(spec, "agents:") {
		return fmt.Errorf("%q: the agents:// scheme is not supported; use baton <provider>[/<rank>]", spec)
	}

	provider, rest, hasRank := strings.Cut(spec, "/")
	if provider == "" {
		return errors.New("missing provider name")
	}
	if !isProviderName(provider) {
		return fmt.Errorf("%q: provider name may only contain letters, digits, '-' and '_'", provider)
	}
	cmd.Target.Provider = provider

	if !hasRank {
		return nil
	}
	if rest == "" || strings.Contains(rest, "/") {
		return fmt.Errorf("%q: expected <provider>/<rank> with a single numeric rank", spec)
	}
	rank, err := strconv.Atoi(rest)
	if err != nil || rank < 1 {
		return fmt.Errorf("%q: rank must be a positive integer; use --id for a session id", spec)
	}
	cmd.Target.Rank = rank
	return nil
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
	switch {
	case t.SessionID != "" && t.Rank > 0:
		return errors.New("--id cannot be combined with a /rank selector")
	case t.SessionID != "" && cmd.List:
		return errors.New("--id cannot be combined with --list")
	case t.SessionID != "" && t.Query != "":
		return errors.New("--id cannot be combined with -q")
	case t.Rank > 0 && cmd.List:
		return errors.New("a /rank selector cannot be combined with --list")
	case cmd.MetaOnly && cmd.List:
		return errors.New("-I cannot be combined with --list")
	case cmd.LastN > 0 && cmd.SinceCompact:
		return errors.New("--last cannot be combined with --since-compact; they are alternative trims")
	}

	// -q implies list mode unless a concrete row was selected by rank or id.
	if t.Query != "" && t.Rank == 0 && t.SessionID == "" {
		cmd.List = true
	}
	return nil
}
