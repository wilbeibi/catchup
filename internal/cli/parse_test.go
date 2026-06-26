package cli

import (
	"testing"

	"github.com/wilbeibi/baton/internal/session"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want Command
	}{
		{
			name: "provider only renders latest as markdown",
			args: []string{"codex"},
			want: Command{Target: session.Target{Provider: "codex"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "flags may precede the target",
			args: []string{"--html", "claude"},
			want: Command{Target: session.Target{Provider: "claude"}, Format: session.FormatHTML, Limit: DefaultLimit},
		},
		{
			name: "numeric rank selects a row",
			args: []string{"codex/2"},
			want: Command{Target: session.Target{Provider: "codex", Rank: 2}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "query alone implies list mode",
			args: []string{"claude", "-q", "deploy"},
			want: Command{Target: session.Target{Provider: "claude", Query: "deploy"}, Format: session.FormatMarkdown, List: true, Limit: DefaultLimit},
		},
		{
			name: "query with rank renders that row, not a list",
			args: []string{"codex/2", "-q", "deploy"},
			want: Command{Target: session.Target{Provider: "codex", Rank: 2, Query: "deploy"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "explicit id escape hatch",
			args: []string{"codex", "--id", "019f-abc"},
			want: Command{Target: session.Target{Provider: "codex", SessionID: "019f-abc"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "list with custom limit",
			args: []string{"opencode", "--list", "-n", "5"},
			want: Command{Target: session.Target{Provider: "opencode"}, Format: session.FormatMarkdown, List: true, Limit: 5},
		},
		{
			name: "last N as json",
			args: []string{"codex", "--json", "--last=20"},
			want: Command{Target: session.Target{Provider: "codex"}, Format: session.FormatJSON, LastN: 20, Limit: DefaultLimit},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.args, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q)\n got = %+v\nwant = %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestParseRejects(t *testing.T) {
	bad := [][]string{
		{},                               // missing provider
		{"agents://codex/latest"},        // legacy scheme
		{"codex/019f-abcdef"},            // session id mistaken as a rank
		{"codex/role/user"},              // path/role form
		{"codex?query=x"},                // query-string form
		{"codex/2", "--list"},            // rank + list conflict
		{"codex", "--id", "x", "--list"}, // id + list conflict
		{"codex", "extra"},               // two targets
		{"codex", "--bogus"},             // unknown flag
		{"codex", "-n", "0"},             // non-positive limit
	}
	for _, args := range bad {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%q) = nil error, want rejection", args)
		}
	}
}
