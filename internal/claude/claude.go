// Package claude implements session.Provider over Claude Code history: project
// JSONL under $CLAUDE_CONFIG_DIR/projects/**/<uuid>.jsonl (default
// ~/.claude).
//
// Useful records: top-level user and assistant entries; message.content as a
// string or as text blocks; compact summaries from the known compact/summary
// system records; ai-title, cwd, gitBranch, sessionId, and timestamp for
// metadata and listing.
//
// Ignored by default: tool_use, tool_result, thinking, queue/mode/permission
// bookkeeping, file-history snapshots, last-prompt, subagent files under
// */subagents/*, and .claude/transcripts/ses_*.jsonl (v1).
package claude

import (
	"context"
	"fmt"

	"github.com/wilbeibi/baton/internal/session"
)

// Provider reads Claude Code project transcripts.
type Provider struct{}

// New returns a Claude provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	return session.Source{}, fmt.Errorf("claude: Resolve not implemented")
}

func (p *Provider) ResolveRank(ctx context.Context, roots session.Roots, opts session.ListOptions, rank int) (session.Source, error) {
	return session.Source{}, fmt.Errorf("claude: ResolveRank not implemented")
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	return session.Thread{}, fmt.Errorf("claude: Read not implemented")
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	return nil, fmt.Errorf("claude: List not implemented")
}
