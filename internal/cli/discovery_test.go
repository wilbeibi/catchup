package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wilbeibi/catchup/internal/session"
)

// Issue #22: selection must preserve the scope and identity seen in discovery.
func TestDiscoveryAcrossDirectories(t *testing.T) {
	roots := codexRoot(t)
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"codex", "--list", "--json"}, false},
		{[]string{"--all-dirs", "-q", "hello", "--json"}, true},
	} {
		out := runWithCwd(t, roots, fxDir("/elsewhere"), tc.args...)
		if strings.Contains(out, "sess-1") != tc.want {
			t.Fatalf("%v: got %s; session present want %v", tc.args, out, tc.want)
		}
	}
	out := runWithCwd(t, roots, fxDir("/elsewhere"), "codex/1", "--all-dirs", "-q", "hello")
	if !strings.Contains(out, "hello from cli test") {
		t.Fatal(out)
	}
	out = runWithCwd(t, roots, fxDir("/elsewhere"), "codex", "--all-dirs", "--list")
	if !strings.Contains(out, fxDir("/home/u/src/proj")) {
		t.Fatalf("directory missing: %s", out)
	}
	for _, args := range [][]string{{"--all-dirs", "--dir", "."}, {"install-skill", "--all-dirs"}, {"fork", "--into", "codex", "--from", "x", "--all-dirs"}} {
		if _, err := Parse(args); err == nil {
			t.Fatalf("%v should reject conflicting scope", args)
		}
	}
}

// The native index and database are external-format fixtures, not renderer types.
func TestCodexNativeNamesAndRelationships(t *testing.T) {
	for _, tc := range []struct{ store, origin, relation, label string }{
		{"index", `"forked_from_id":"parent-1"`, "fork", "fork of: parent-1"},
		{"database", `"source":{"subagent":{"thread_spawn":{"parent_thread_id":"parent-1","agent_role":"reviewer"}}}`, "child", "child of: parent-1 (reviewer)"},
		{"legacy_database", `"forked_from_id":"parent-1"`, "fork", "fork of: parent-1"},
	} {
		t.Run(tc.store, func(t *testing.T) {
			roots := codexRoot(t)
			path := filepath.Join(roots.Codex, "session_index.jsonl")
			if err := os.WriteFile(path, []byte("{\"id\":\"sess-1\",\"thread_name\":\"Old label\"}\n{\"id\":\"sess-1\",\"thread_name\":\"Cache investigation\"}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.store != "index" {
				db, err := sql.Open("sqlite", filepath.Join(roots.Codex, "state_5.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				queries := []string{"CREATE TABLE threads (id TEXT, title TEXT, name TEXT)", "INSERT INTO threads VALUES ('sess-1','Old label','Cache investigation')"}
				if tc.store == "legacy_database" {
					queries = []string{"CREATE TABLE threads (id TEXT, title TEXT)", "INSERT INTO threads VALUES ('sess-1','Old label')"}
				}
				for _, q := range queries {
					if _, err = db.Exec(q); err != nil {
						t.Fatal(err)
					}
				}
				db.Close()
				if err := func() error {
					if tc.store != "database" {
						return nil
					}
					return os.WriteFile(path, []byte("{\"id\":\"sess-1\",\"thread_name\":\"Stale index\"}\n"), 0600)
				}(); err != nil {
					t.Fatal(err)
				}
			}
			file := filepath.Join(roots.Codex, "sessions", "2026", "06", "26", "rollout-sess-1.jsonl")
			b, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			body := strings.Replace(string(b), `"cli_version":"0.1"`, `"cli_version":"0.1",`+tc.origin, 1)
			if err := os.WriteFile(file, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			out := run(t, roots, "codex", "-q", "cache investigation", "--json")
			var rows []struct {
				Title, Parent, Relationship string
				Match                       struct{ Kind, Role, Text string }
			}
			if err := json.Unmarshal([]byte(out), &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].Title != "Cache investigation" || rows[0].Match.Kind != "title" || rows[0].Match.Role != "" || rows[0].Parent != "parent-1" || rows[0].Relationship != tc.relation {
				t.Fatalf("native metadata: %s", out)
			}
			out = run(t, roots, "codex/1", "-q", "cache investigation")
			if !strings.Contains(out, "hi back") {
				t.Fatalf("title match must read conversation: %s", out)
			}
			out = run(t, roots, "codex", "-q", "cache investigation")
			if !strings.Contains(out, "title: Cache investigation") || !strings.Contains(out, tc.label) {
				t.Fatalf("unlabeled match or relationship: %s", out)
			}
			if out = run(t, roots, "codex", "-q", "Old label", "--json"); strings.TrimSpace(out) != "[]" {
				t.Fatalf("stale title matched: %s", out)
			}
		})
	}
}

func TestAmpLocalDiscoveryAndRead(t *testing.T) {
	roots := session.Roots{Amp: t.TempDir()}
	dir := filepath.Join(roots.Amp, "threads")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Shape from Amp's cached thread format; all content is synthetic.
	body := `{"v":7,"id":"T-example","title":"Investigate cache","env":{"initial":{"trees":[{"uri":"file:///project%20one"}]}},"relationships":[{"type":"handoff","role":"child","threadID":"T-parent"}],"messages":[{"role":"system","content":"hidden system"},{"role":"user","content":[{"type":"text","text":"explain eviction"}],"meta":{"sentAt":1771304680667}},{"role":"assistant","content":[{"type":"thinking","thinking":"hidden thought"},{"type":"text","text":"use a bounded cache"},{"type":"tool_use","name":"bash","input":{"command":"hidden command"}}]},{"role":"user","content":[{"type":"tool_result","content":"hidden result"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "T-example.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	out := runWithCwd(t, roots, "/elsewhere", "--all-dirs", "-q", "Investigate cache", "--json")
	if !strings.Contains(out, "T-example") || !strings.Contains(out, "project one") || !strings.Contains(out, "T-parent") {
		t.Fatal(out)
	}
	out = run(t, roots, "amp", "--id", "T-example", "-q", "bounded", "--json")
	if !strings.Contains(out, "explain eviction") || !strings.Contains(out, "use a bounded cache") || strings.Contains(out, "hidden") {
		t.Fatalf("conversation projection: %s", out)
	}
	p, _ := selectProvider("amp")
	if _, err := p.Resolve(context.Background(), roots, "../T-example"); err == nil {
		t.Fatal("unknown id resolved")
	}
	if _, _, err := forkCommand(session.Source{Ref: session.Ref{Provider: "amp", SessionID: "T-example"}}, ""); err == nil {
		t.Fatal("Amp native resume should be unsupported")
	}
}
