package cli

import (
	"testing"

	"github.com/wilbeibi/catchup/internal/session"
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
			name: "bare invocation leaves the provider for detection",
			args: []string{},
			want: Command{Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "bare invocation accepts flags",
			args: []string{"--list"},
			want: Command{Format: session.FormatMarkdown, List: true, Limit: DefaultLimit},
		},
		{
			name: "fork latest across providers",
			args: []string{"fork"},
			want: Command{Action: "fork", Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork latest provider",
			args: []string{"fork", "codex"},
			want: Command{Action: "fork", Target: session.Target{Provider: "codex"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork into another agent",
			args: []string{"fork", "codex", "--into", "claude"},
			want: Command{Action: "fork", Into: "claude", Target: session.Target{Provider: "codex"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork into allows trims",
			args: []string{"fork", "--into", "claude", "--since-compact"},
			want: Command{Action: "fork", Into: "claude", SinceCompact: true, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "--into with model",
			args: []string{"fork", "claude", "--into", "codex", "--model", "gpt-5.6"},
			want: Command{Action: "fork", Into: "codex", Model: "gpt-5.6", Target: session.Target{Provider: "claude"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "model on a native fork",
			args: []string{"fork", "codex", "--model", "gpt-5.6-mini"},
			want: Command{Action: "fork", Model: "gpt-5.6-mini", Target: session.Target{Provider: "codex"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "install-skill for every provider",
			args: []string{"install-skill"},
			want: Command{Action: "install-skill", Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "install-skill for one provider",
			args: []string{"install-skill", "codex"},
			want: Command{Action: "install-skill", Target: session.Target{Provider: "codex"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
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
		{
			name: "--help with provider",
			args: []string{"codex", "--help"},
			want: Command{Help: true, Limit: DefaultLimit, Format: session.FormatMarkdown},
		},
		{
			name: "-h with provider",
			args: []string{"claude", "-h"},
			want: Command{Help: true, Limit: DefaultLimit, Format: session.FormatMarkdown},
		},
		{
			name: "--help alone, no provider",
			args: []string{"--help"},
			want: Command{Help: true, Limit: DefaultLimit, Format: session.FormatMarkdown},
		},
		{
			name: "--help before target",
			args: []string{"--help", "claude"},
			want: Command{Help: true, Limit: DefaultLimit, Format: session.FormatMarkdown},
		},
		{
			name: "--limit sets limit in query mode",
			args: []string{"codex", "-q", "auth", "--limit", "5"},
			want: Command{Target: session.Target{Provider: "codex", Query: "auth"}, Format: session.FormatMarkdown, List: true, Limit: 5},
		},
		{
			name: "-i sets meta-only",
			args: []string{"codex", "-i"},
			want: Command{Target: session.Target{Provider: "codex"}, Format: session.FormatMarkdown, MetaOnly: true, Limit: DefaultLimit},
		},
		{
			name: "--info sets meta-only",
			args: []string{"codex", "--info"},
			want: Command{Target: session.Target{Provider: "codex"}, Format: session.FormatMarkdown, MetaOnly: true, Limit: DefaultLimit},
		},
		{
			name: "--full disables clamping",
			args: []string{"codex", "--full"},
			want: Command{Target: session.Target{Provider: "codex"}, Format: session.FormatMarkdown, Full: true, Limit: DefaultLimit},
		},
		{
			name: "fork selects by rank",
			args: []string{"fork", "codex/3"},
			want: Command{Action: "fork", Target: session.Target{Provider: "codex", Rank: 3}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork selects by id",
			args: []string{"fork", "codex", "--id", "019f-abc"},
			want: Command{Action: "fork", Target: session.Target{Provider: "codex", SessionID: "019f-abc"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork query does not imply list mode",
			args: []string{"fork", "codex", "-q", "auth"},
			want: Command{Action: "fork", Target: session.Target{Provider: "codex", Query: "auth"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork into allows --full on the seed",
			args: []string{"fork", "codex", "--into", "claude", "--full"},
			want: Command{Action: "fork", Into: "claude", Full: true, Target: session.Target{Provider: "codex"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "--dir substitutes the selection directory",
			args: []string{"claude", "--dir", "/home/u/proj"},
			want: Command{Dir: "/home/u/proj", Target: session.Target{Provider: "claude"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork into from a file artifact",
			args: []string{"fork", "--into", "claude", "--from", "handoff.md"},
			want: Command{Action: "fork", Into: "claude", From: "handoff.md", Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork from stdin with a model",
			args: []string{"fork", "--into", "codex", "--from=-", "--model", "gpt-5.6"},
			want: Command{Action: "fork", Into: "codex", From: "-", Model: "gpt-5.6", Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork from an https url",
			args: []string{"fork", "--into", "claude", "--from", "https://box.ts.net/s.md"},
			want: Command{Action: "fork", Into: "claude", From: "https://box.ts.net/s.md", Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork from allows same-agent into",
			args: []string{"fork", "--into", "claude", "--from", "~/notes/handoff.md"},
			want: Command{Action: "fork", Into: "claude", From: "~/notes/handoff.md", Format: session.FormatMarkdown, Limit: DefaultLimit},
		},
		{
			name: "fork composes with --dir",
			args: []string{"fork", "claude", "--dir", "../proj"},
			want: Command{Action: "fork", Dir: "../proj", Target: session.Target{Provider: "claude"}, Format: session.FormatMarkdown, Limit: DefaultLimit},
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
		{"--id", "x"},                                                     // --id needs an explicit agent
		{"agents://codex/latest"},                                         // legacy scheme
		{"codex/019f-abcdef"},                                             // session id mistaken as a rank
		{"codex/role/user"},                                               // path/role form
		{"codex?query=x"},                                                 // query-string form
		{"codex/2", "--list"},                                             // rank + list conflict
		{"codex", "--id", "x", "--list"},                                  // id + list conflict
		{"codex", "extra"},                                                // two targets
		{"codex", "--bogus"},                                              // unknown flag
		{"codex", "-n", "0"},                                              // non-positive limit
		{"codex", "-n", "5"},                                              // -n without a listing
		{"-n", "5"},                                                       // -n without a listing, bare form
		{"fork", "codex", "-n", "5"},                                      // -n without a listing, action form
		{"claude", "59d0fbfa-5187-421b"},                                  // session id pasted as a second target
		{"fork", "codex", "--list"},                                       // fork is not a render mode; -q covers listing needs
		{"fork", "codex", "--last", "1"},                                  // fork is not a trim mode
		{"fork", "--last", "1"},                                           // same rejection without provider
		{"fork", "codex", "--full"},                                       // --full only shapes an --into seed
		{"codex", "--into", "claude"},                                     // --into only applies to fork
		{"fork", "codex", "--into", "claude", "--list"},                   // --into is not a render mode
		{"install-skill", "codex", "--into", "claude"},                    // --into only applies to fork
		{"install-skill", "codex/2"},                                      // install-skill does not take a rank
		{"install-skill", "codex", "--list"},                              // install-skill is not a render mode
		{"install-skill", "codex", "-q", "x"},                             // install-skill does not take selectors
		{"claude", "--model", "gpt-5.6"},                                  // --model only applies to fork
		{"claude", "--full", "--json"},                                    // --json is never clamped
		{"claude", "--full", "--list"},                                    // listings show no bodies to clamp
		{"claude", "--full", "-i"},                                        // -i shows no bodies to clamp
		{"install-skill", "codex", "--dir", "/x"},                         // install-skill takes no scope
		{"claude", "--id", "x", "--dir", "/y"},                            // --id already names one session
		{"claude", "--dir", "box:/home/u/proj"},                           // scp syntax: --dir is local-only
		{"claude", "--from", "s.md"},                                      // --from only applies to fork --into
		{"fork", "--from", "s.md"},                                        // --from needs --into: nothing to resume
		{"fork", "codex", "--into", "claude", "--from", "s.md"},           // an agent name selects a store
		{"fork", "codex/2", "--into", "claude", "--from", "s.md"},         // a rank selects from a store
		{"fork", "--into", "claude", "--from", "s.md", "-q", "auth"},      // -q selects from a store
		{"fork", "--into", "claude", "--from", "s.md", "--id", "x"},       // --id selects from a store
		{"fork", "--into", "claude", "--from", "s.md", "--dir", "/x"},     // --dir scopes stores, not artifacts
		{"fork", "--into", "claude", "--from", "s.md", "--last", "5"},     // artifacts seed verbatim
		{"fork", "--into", "claude", "--from", "s.md", "--since-compact"}, // artifacts seed verbatim
		{"fork", "--into", "claude", "--from", "s.md", "--full"},          // artifacts are never re-clamped
		{"fork", "--into", "claude", "--from", "s3://bucket/s.md"},        // per-service schemes are transports
		{"fork", "--into", "claude", "--from", "file:///home/u/s.md"},     // use the plain path
		{"fork", "--into", "claude", "--from", "box:s.md"},                // scp syntax: pipe over ssh instead
		{"fork", "--into", "claude", "--from", ""},                        // empty value
		{"install-skill", "codex", "--from", "s.md"},                      // install-skill takes nothing
	}
	for _, args := range bad {
		if _, err := Parse(args); err == nil {
			t.Errorf("Parse(%q) = nil error, want rejection", args)
		}
	}
}
