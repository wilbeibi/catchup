// Package codex implements session.Provider over Codex CLI history: rollout JSONL
// files under $CODEX_HOME (default ~/.codex).
//
// Useful records: session_meta.payload.{id,cwd,timestamp,cli_version,
// model_provider} for metadata; response_item.payload with type=message and
// role user/assistant, content types input_text and output_text, for the
// timeline; type=compacted and event_msg.payload.type=context_compacted as
// compaction markers.
//
// Ignored by default: function_call, function_call_output, custom_tool_call,
// web_search_call, MCP/tool events, patches, token counts, rate limits, memory
// citations, encrypted reasoning, turn_context, base instructions, and
// developer-role messages. event_msg.agent_message is a fallback only when
// canonical response_item messages are absent.
package codex

import (
	"context"
	"fmt"

	"github.com/wilbeibi/baton/internal/session"
)

// Provider reads Codex rollout files. It holds no state today; the receiver is
// kept so per-instance configuration (clock, fs override) can be added without
// changing the constructor's callers.
type Provider struct{}

// New returns a Codex provider.
func New() *Provider { return &Provider{} }

// Ensure Provider satisfies the interface at compile time.
var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	return session.Source{}, fmt.Errorf("codex: Resolve not implemented")
}

func (p *Provider) ResolveRank(ctx context.Context, roots session.Roots, opts session.ListOptions, rank int) (session.Source, error) {
	return session.Source{}, fmt.Errorf("codex: ResolveRank not implemented")
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	return session.Thread{}, fmt.Errorf("codex: Read not implemented")
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	return nil, fmt.Errorf("codex: List not implemented")
}
