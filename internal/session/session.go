// Package session defines the vocabulary shared by every layer of the tool: the
// data types that describe a located agent session, and the Provider interface
// that maps a user's request onto one of those sessions.
//
// This package performs no I/O and knows nothing about the CLI or about output
// formats. The provider packages (internal/codex, internal/claude,
// internal/opencode, internal/piagent) implement Provider against real history
// on disk; the cli layer turns user input into a request and hands the result to
// the renderer (internal/render). Keeping the nouns here, free of any behavior,
// is what lets the other layers stay independent of one another.
package session

import (
	"context"
	"time"
)

// Provider names. These are the only legal first segment of a target and the
// keys of the provider registry.
const (
	ProviderCodex    = "codex"
	ProviderClaude   = "claude"
	ProviderOpenCode = "opencode"
	ProviderPiAgent  = "pi-agent"
)

// Entry kinds and message roles. Providers normalize their own wire formats
// onto these so the renderer never sees provider-specific strings.
const (
	KindMessage = "message"
	KindCompact = "compact"

	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Ref identifies one conversation within one provider. It is the stable handle
// that survives across runs: SessionID is the provider's own identifier, the
// value a user passes to --id.
type Ref struct {
	Provider  string
	SessionID string
}

// Target is the structured form of a user's selection request, produced by the
// CLI parser. Exactly one selection mode is active, in this precedence:
//
//	SessionID != "" : the exact session (the --id escape hatch)
//	Rank > 0        : the Rank-th row of the current listing (1-based)
//	otherwise       : the newest session
//
// Query narrows the listing that Rank indexes into. (At the cli layer, a Query
// with no Rank also switches the command into list mode.)
type Target struct {
	Provider  string
	Rank      int
	SessionID string
	Query     string
}

// Roots are the resolved on-disk locations of each provider's history. An empty
// field means the provider default was not overridden; ResolveRoots always
// fills every field with an absolute path.
type Roots struct {
	Codex    string
	Claude   string
	OpenCode string
	PiAgent  string
}

// Source is a located session: enough to read it and to describe it in a
// listing without yet parsing its full timeline. Metadata is deliberately
// shallow (string to string) so providers stay honest about what they surface;
// it is never a place to stash structured payloads.
type Source struct {
	Ref       Ref
	Path      string
	UpdatedAt time.Time
	Metadata  map[string]string
	Warnings  []string
}

// Entry is one visible item on the conversation timeline. Kind is KindMessage
// or KindCompact; for messages, Role is RoleUser or RoleAssistant. Tool calls,
// tool results, reasoning, and bookkeeping never become Entries.
type Entry struct {
	Kind string
	Role string
	Text string
	Time time.Time
}

// Thread is a fully read session: its Source plus the ordered, visible
// timeline. Warnings collects anything skipped or recovered during reading that
// a user might want to know about (truncated records, unknown content types).
type Thread struct {
	Source   Source
	Entries  []Entry
	Warnings []string
}

// Summary is one row of a listing: a Source projected for display, carrying the
// 1-based Rank that will re-select it on a later invocation.
type Summary struct {
	Ref       Ref
	Rank      int
	UpdatedAt time.Time
	Title     string
	Cwd       string
	Preview   string
}

// ListOptions controls listing and rank resolution. Query is a literal,
// case-insensitive match over visible text. Cwd filters to sessions whose
// working directory matches exactly; empty means no directory filter. Limit caps
// the number of rows; zero means the caller should apply its default.
type ListOptions struct {
	Query string
	Cwd   string
	Limit int
}

// Provider maps a user's request onto a concrete session for one agent tool.
// The four methods are orthogonal on purpose: Resolve and ResolveRank only
// locate a Source, Read only parses one Source into a Thread, and List only
// enumerates Sources for display. Implementations touch the filesystem or a
// database; none of them format output.
type Provider interface {
	// Resolve returns the newest session when id is empty, or the session with
	// exactly that id otherwise.
	Resolve(ctx context.Context, roots Roots, id string) (Source, error)

	// ResolveRank returns the rank-th Source (1-based) from the same ordering
	// List would produce for opts.
	ResolveRank(ctx context.Context, roots Roots, opts ListOptions, rank int) (Source, error)

	// Read parses a located Source into its visible timeline.
	Read(ctx context.Context, src Source) (Thread, error)

	// List enumerates sessions newest-first, filtered and capped by opts.
	List(ctx context.Context, roots Roots, opts ListOptions) ([]Summary, error)
}

// Format selects an output encoding for the renderer.
type Format int

const (
	FormatMarkdown Format = iota // YAML frontmatter + numbered timeline (default)
	FormatHTML                   // self-contained HTML, inline CSS, no JavaScript
	FormatJSON                   // structured Thread/Source as JSON
)

// String returns the canonical lowercase name of the format.
func (f Format) String() string {
	switch f {
	case FormatMarkdown:
		return "markdown"
	case FormatHTML:
		return "html"
	case FormatJSON:
		return "json"
	default:
		return "unknown"
	}
}
