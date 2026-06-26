// Package opencode implements session.Provider over OpenCode history stored in a
// SQLite database at <XDG_DATA_HOME|~/.local/share>/opencode/opencode.db.
//
// This is the only provider that needs an external dependency. The
// implementation will open the database read-only via modernc.org/sqlite (a
// pure-Go, cgo-free driver) so the rest of the program stays dependency-free
// and SQLite stays isolated to this package.
//
// Useful rows: session.{id,title,directory,parent_id,agent,model,time_created,
// time_updated} for metadata and listing; message.{id,session_id,time_created,
// data} for role and order; part.data.type=text for timeline text;
// part.data.type=compaction as a compaction marker.
//
// Ignored by default: part.type=tool (it dominates database size), reasoning,
// step-start, step-finish, token/cost plumbing, snapshots, patch payloads, and
// raw file payloads. Child session ids can be read, but v1 does not expand
// parent/child trees.
package opencode

import (
	"context"
	"fmt"

	"github.com/wilbeibi/baton/internal/session"
)

// Provider reads the OpenCode SQLite database. A future field will hold an
// *sql.DB opened lazily and closed by the caller; for now it is stateless.
type Provider struct{}

// New returns an OpenCode provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	return session.Source{}, fmt.Errorf("opencode: Resolve not implemented")
}

func (p *Provider) ResolveRank(ctx context.Context, roots session.Roots, opts session.ListOptions, rank int) (session.Source, error) {
	return session.Source{}, fmt.Errorf("opencode: ResolveRank not implemented")
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	return session.Thread{}, fmt.Errorf("opencode: Read not implemented")
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	return nil, fmt.Errorf("opencode: List not implemented")
}
