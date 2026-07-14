package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

const rollout = `{"timestamp":"2026-06-26T21:31:46.0Z","type":"session_meta","payload":{"id":"sess-1","cwd":"/home/u/src/proj","cli_version":"0.1"}}
{"timestamp":"2026-06-26T21:31:55.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello from cli test"}]}}
{"timestamp":"2026-06-26T21:32:08.0Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi back"}]}}
`

// codexRoot writes a single codex rollout fixture and returns a Roots pointing at
// it, exercising the real codex provider through cli.Run.
func codexRoot(t *testing.T) session.Roots {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "06", "26")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-sess-1.jsonl"), []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}
	return session.Roots{Codex: root}
}

func run(t *testing.T, roots session.Roots, args ...string) string {
	t.Helper()
	return runWithCwd(t, roots, "", args...)
}

// claudeRoot writes two Claude transcripts sharing one working directory and
// returns a Roots pointing at them. sess-new is given the newer mtime, so
// "newest in cwd" resolves to it; a test can then prove that an injected
// current-session id (sess-old) wins over recency.
func claudeRoot(t *testing.T) session.Roots {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(id, text string, mod time.Time) {
		lines := fmt.Sprintf(
			`{"type":"user","sessionId":%q,"cwd":"/home/u/proj","timestamp":"2026-06-26T10:00:00Z","message":{"role":"user","content":%q}}
{"type":"assistant","sessionId":%q,"cwd":"/home/u/proj","timestamp":"2026-06-26T10:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}
`, id, text, id)
		p := filepath.Join(dir, id+".jsonl")
		if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	write("sess-old", "old session question", now.Add(-time.Hour))
	write("sess-new", "new session question", now)
	return session.Roots{Claude: root}
}

func piAgentRoot(t *testing.T) session.Roots {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "--home-u-src-proj--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"session","version":3,"id":"pi-1","timestamp":"2026-06-28T02:50:19.365Z","cwd":"/home/u/src/proj"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-06-28T02:50:23.414Z","message":{"role":"user","content":[{"type":"text","text":"hello pi"}]}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-06-28T02:50:26.242Z","message":{"role":"assistant","content":[{"type":"text","text":"hi pi"}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "2026-06-28T02-50-19-365Z_pi-1.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return session.Roots{PiAgent: root}
}

func runWithCwd(t *testing.T, roots session.Roots, cwd string, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := Run(context.Background(), args, roots, nil, nil, nil, "test", cwd, nil, &out, &errOut); err != nil {
		t.Fatalf("Run(%v) error: %v (stderr: %s)", args, err, errOut.String())
	}
	return out.String()
}

// fakeProvider serves a fixed newest-first listing so resolveRank's
// composition can be tested without fixtures.
type fakeProvider struct{ ids []string }

func (f fakeProvider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	for _, x := range f.ids {
		if x == id {
			return session.Source{Ref: session.Ref{Provider: "fake", SessionID: id}}, nil
		}
	}
	return session.Source{}, fmt.Errorf("fake: no session with id %q", id)
}

func (f fakeProvider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	return session.Thread{Source: src}, nil
}

func (f fakeProvider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	var out []session.Summary
	for i, id := range f.ids {
		if len(out) >= opts.EffectiveLimit() {
			break
		}
		out = append(out, session.Summary{Ref: session.Ref{Provider: "fake", SessionID: id}, Rank: i + 1})
	}
	return out, nil
}

func TestResolveRank(t *testing.T) {
	ctx := context.Background()
	prov := fakeProvider{ids: []string{"newest", "middle", "oldest"}}

	src, err := resolveRank(ctx, prov, "fake", session.Roots{}, session.ListOptions{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "middle" {
		t.Errorf("rank 2 = %s, want middle", src.Ref.SessionID)
	}
	if _, err := resolveRank(ctx, prov, "fake", session.Roots{}, session.ListOptions{}, 9); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Errorf("want out-of-range error, got %v", err)
	}
	if _, err := resolveRank(ctx, prov, "fake", session.Roots{}, session.ListOptions{}, 0); err == nil || !strings.Contains(err.Error(), "rank must be >= 1") {
		t.Errorf("want rank-must-be-positive error, got %v", err)
	}
}

// A directory with no sessions must produce a next step, not a dead end: the
// error names the directory that has the provider's newest session.
func TestEmptyStateHint(t *testing.T) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"codex"}, codexRoot(t), nil, nil, nil, "test", "/somewhere/else", nil, &out, &errOut)
	if err == nil {
		t.Fatal("want error when cwd has no sessions")
	}
	for _, want := range []string{"no sessions in /somewhere/else", "newest is in /home/u/src/proj"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestListJSON(t *testing.T) {
	out := runWithCwd(t, codexRoot(t), "/home/u/src/proj", "codex", "--list", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("listing is not JSON: %v\n%s", err, out)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(rows), rows)
	}
	r := rows[0]
	if r["session_id"] != "sess-1" || r["agent"] != "codex" || r["rank"] != float64(1) || r["cwd"] != "/home/u/src/proj" {
		t.Errorf("row = %+v", r)
	}
}

func TestListHTMLRejected(t *testing.T) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"codex", "--list", "--html"}, codexRoot(t), nil, nil, nil, "test", "", nil, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "--html does not apply to listings") {
		t.Errorf("want listing-format rejection, got %v", err)
	}
}

func TestRunCwdFiltering(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "06", "26")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	catchupRollout := `{"timestamp":"2026-06-26T21:31:46.0Z","type":"session_meta","payload":{"id":"sess-catchup","cwd":"/home/u/src/catchup","cli_version":"0.1"}}
{"timestamp":"2026-06-26T21:31:55.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"refactor the auth module"}]}}
{"timestamp":"2026-06-26T21:32:08.0Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}}
`
	otherRollout := `{"timestamp":"2026-06-26T22:00:00.0Z","type":"session_meta","payload":{"id":"sess-other","cwd":"/home/u/src/other","cli_version":"0.1"}}
{"timestamp":"2026-06-26T22:01:00.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"unrelated work"}]}}
{"timestamp":"2026-06-26T22:02:00.0Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "rollout-sess-catchup.jsonl"), []byte(catchupRollout), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-sess-other.jsonl"), []byte(otherRollout), 0o644); err != nil {
		t.Fatal(err)
	}
	roots := session.Roots{Codex: root}

	// With cwd: only the matching session appears.
	out := runWithCwd(t, roots, "/home/u/src/catchup", "codex", "--list")
	if !strings.Contains(out, "sess-catchup") {
		t.Errorf("expected sess-catchup in cwd-filtered listing, got:\n%s", out)
	}
	if strings.Contains(out, "sess-other") {
		t.Errorf("unexpected sess-other in cwd-filtered listing, got:\n%s", out)
	}

	// --dir substitutes another directory for the cwd.
	out = runWithCwd(t, roots, "/somewhere/else", "codex", "--list", "--dir", "/home/u/src/other")
	if !strings.Contains(out, "sess-other") || strings.Contains(out, "sess-catchup") {
		t.Errorf("--dir should select exactly the named directory, got:\n%s", out)
	}
}

// The worktree recipe: from a fresh directory, --dir points the fork source
// back at the directory where the sessions actually live.
func TestRunForkFromAnotherDir(t *testing.T) {
	roots := codexPairRoot(t) // sessions live in /home/u/src/proj
	var got session.Source
	withForkRunner(t, func(ctx context.Context, src session.Source, model string, stdin io.Reader, stdout, stderr io.Writer) error {
		got = src
		return nil
	})
	var out, errOut bytes.Buffer
	args := []string{"fork", "codex", "--dir", "/home/u/src/proj"}
	if err := Run(context.Background(), args, roots, nil, nil, nil, "test", "/somewhere/worktree", nil, &out, &errOut); err != nil {
		t.Fatalf("Run(%v) error: %v (stderr: %s)", args, err, errOut.String())
	}
	if got.Ref.SessionID != "sess-parser" {
		t.Fatalf("fork --dir dispatched %+v, want newest in named dir (sess-parser)", got.Ref)
	}
}

func TestExpandTilde(t *testing.T) {
	t.Setenv("HOME", "/home/test")
	tests := []struct{ in, want string }{
		{"~", "/home/test"},
		{"~/proj", "/home/test/proj"},
		{"/abs/path", "/abs/path"},
		{"~user/x", "~user/x"}, // other users' homes are not resolved
	}
	for _, tt := range tests {
		if got := expandTilde(tt.in); got != tt.want {
			t.Errorf("expandTilde(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRunRendersLatestMarkdown(t *testing.T) {
	out := run(t, codexRoot(t), "codex")
	for _, want := range []string{"agent: codex", "session: sess-1", "## 1. user", "hello from cli test", "## 2. assistant", "hi back"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRunRendersPiAgentMarkdown(t *testing.T) {
	out := run(t, piAgentRoot(t), "pi-agent")
	for _, want := range []string{"agent: pi-agent", "session: pi-1", "## 1. user", "hello pi", "## 2. assistant", "hi pi"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestLastTurns(t *testing.T) {
	// Keeping the last turn must start at the final user message and include
	// every assistant reply that follows it — not the prior exchange or the
	// compaction marker before it.
	u := func(s string) session.Entry {
		return session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: s}
	}
	a := func(s string) session.Entry {
		return session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: s}
	}
	compact := session.Entry{Kind: session.KindCompact}

	thread := session.Thread{Entries: []session.Entry{
		u("q1"), a("a1"),
		compact,
		u("q2"), a("a2a"), a("a2b"),
	}}

	got := lastTurns(thread, 1)
	want := []string{"q2", "a2a", "a2b"}
	if len(got.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got.Entries), len(want))
	}
	for i, e := range got.Entries {
		if e.Text != want[i] {
			t.Errorf("entry %d = %q, want %q", i, e.Text, want[i])
		}
	}
}

func TestSinceCompact(t *testing.T) {
	u := func(s string) session.Entry {
		return session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: s}
	}
	a := func(s string) session.Entry {
		return session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: s}
	}
	summary := session.Entry{Kind: session.KindCompact, Text: "recap so far"}

	// Keep the last compaction entry (the recap) and everything after it.
	thread := session.Thread{Entries: []session.Entry{
		u("q1"), a("a1"),
		summary,
		u("q2"), a("a2"),
	}}
	got := sinceCompact(thread)
	want := []string{"recap so far", "q2", "a2"}
	if len(got.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got.Entries), len(want))
	}
	for i, e := range got.Entries {
		if e.Text != want[i] {
			t.Errorf("entry %d = %q, want %q", i, e.Text, want[i])
		}
	}

	// No compaction marker: the whole thread is returned unchanged.
	plain := session.Thread{Entries: []session.Entry{u("q1"), a("a1")}}
	if got := sinceCompact(plain); len(got.Entries) != 2 {
		t.Errorf("no-compaction: got %d entries, want 2", len(got.Entries))
	}
}

func TestCurrentSessionBeatsNewest(t *testing.T) {
	roots := claudeRoot(t)
	cwd := "/home/u/proj"

	// With no injected current id, the default selection is the newest session
	// in cwd.
	out := runWithCwd(t, roots, cwd, "claude")
	if !strings.Contains(out, "session: sess-new") {
		t.Fatalf("default should resolve newest-in-cwd, got:\n%s", out)
	}

	// Claude injects the live session's id; it must win over recency so the
	// right one of several sessions sharing a directory is picked.
	var got, errOut bytes.Buffer
	current := map[string]string{session.ProviderClaude: "sess-old"}
	if err := Run(context.Background(), []string{"claude"}, roots, current, nil, nil, "test", cwd, nil, &got, &errOut); err != nil {
		t.Fatalf("Run error: %v (stderr: %s)", err, errOut.String())
	}
	if !strings.Contains(got.String(), "session: sess-old") {
		t.Errorf("injected current id should win over newest, got:\n%s", got.String())
	}
	if strings.Contains(got.String(), "sess-new") {
		t.Errorf("newest session leaked despite injected current id:\n%s", got.String())
	}

	// An explicit --dir outranks the injected current id: pointing at a
	// directory means pointing away from the session this process sits in.
	var dirOut bytes.Buffer
	if err := Run(context.Background(), []string{"claude", "--dir", "/home/u/proj"}, roots, current, nil, nil, "test", cwd, nil, &dirOut, &errOut); err != nil {
		t.Fatalf("Run --dir error: %v (stderr: %s)", err, errOut.String())
	}
	if !strings.Contains(dirOut.String(), "session: sess-new") {
		t.Errorf("--dir should fall back to newest-in-dir, not the injected current id, got:\n%s", dirOut.String())
	}
}

func TestRunUnknownProvider(t *testing.T) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"bogus"}, session.Roots{}, nil, nil, nil, "test", "", nil, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("expected unknown agent error, got %v", err)
	}
}

func TestRunForkProvider(t *testing.T) {
	roots := codexRoot(t)
	var got session.Source
	withForkRunner(t, func(ctx context.Context, src session.Source, model string, stdin io.Reader, stdout, stderr io.Writer) error {
		got = src
		return nil
	})

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"fork", "codex"}, roots, nil, nil, nil, "test", "/home/u/src/proj", nil, &out, &errOut); err != nil {
		t.Fatalf("Run fork error: %v (stderr: %s)", err, errOut.String())
	}
	if got.Ref.Provider != session.ProviderCodex || got.Ref.SessionID != "sess-1" {
		t.Fatalf("fork dispatched %+v, want codex sess-1", got.Ref)
	}
}

// multiProviderRoots writes an older codex session and a newer pi-agent
// session sharing one working directory, so "newest across providers" must
// pick pi-agent. Used by both the bare fork and the bare read tests.
func multiProviderRoots(t *testing.T) session.Roots {
	t.Helper()
	now := time.Now()
	roots := session.Roots{
		Codex:    t.TempDir(),
		Claude:   t.TempDir(),
		OpenCode: t.TempDir(),
		PiAgent:  t.TempDir(),
	}

	codexDir := filepath.Join(roots.Codex, "sessions", "2026", "06", "26")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(codexDir, "rollout-old-codex.jsonl")
	codexBody := `{"timestamp":"2026-06-26T21:31:46.0Z","type":"session_meta","payload":{"id":"old-codex","cwd":"/home/u/src/proj","cli_version":"0.1"}}
{"timestamp":"2026-06-26T21:31:55.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"old codex"}]}}
`
	if err := os.WriteFile(codexPath, []byte(codexBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(codexPath, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}

	piDir := filepath.Join(roots.PiAgent, "sessions", "--home-u-src-proj--")
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatal(err)
	}
	piPath := filepath.Join(piDir, "2026-06-28T02-50-19-365Z_new-pi.jsonl")
	piBody := `{"type":"session","version":3,"id":"new-pi","timestamp":"2026-06-28T02:50:19.365Z","cwd":"/home/u/src/proj"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-06-28T02:50:23.414Z","message":{"role":"user","content":[{"type":"text","text":"new pi"}]}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-06-28T02:50:26.242Z","message":{"role":"assistant","content":[{"type":"text","text":"hi new pi"}]}}
`
	if err := os.WriteFile(piPath, []byte(piBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(piPath, now, now); err != nil {
		t.Fatal(err)
	}
	return roots
}

func TestRunForkLatestAcrossProviders(t *testing.T) {
	roots := multiProviderRoots(t)

	var got session.Source
	withForkRunner(t, func(ctx context.Context, src session.Source, model string, stdin io.Reader, stdout, stderr io.Writer) error {
		got = src
		return nil
	})

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"fork"}, roots, nil, nil, nil, "test", "/home/u/src/proj", nil, &out, &errOut); err != nil {
		t.Fatalf("Run fork latest error: %v (stderr: %s)", err, errOut.String())
	}
	if got.Ref.Provider != session.ProviderPiAgent || got.Ref.SessionID != "new-pi" {
		t.Fatalf("fork dispatched %+v, want pi-agent new-pi", got.Ref)
	}
}

func TestRunBareReadsLatestAcrossProviders(t *testing.T) {
	roots := multiProviderRoots(t)
	out := runWithCwd(t, roots, "/home/u/src/proj", "--last", "1")
	for _, want := range []string{"agent: pi-agent", "session: new-pi", "new pi"} {
		if !strings.Contains(out, want) {
			t.Errorf("bare read missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "old codex") {
		t.Errorf("bare read picked the older codex session:\n%s", out)
	}
}

func TestForkCommand(t *testing.T) {
	tests := []struct {
		name  string
		src   session.Source
		model string
		want  string
	}{
		{"codex", session.Source{Ref: session.Ref{Provider: session.ProviderCodex, SessionID: "c1"}}, "", "codex fork c1"},
		{"claude", session.Source{Ref: session.Ref{Provider: session.ProviderClaude, SessionID: "cl1"}}, "", "claude --resume cl1 --fork-session"},
		{"claude with model", session.Source{Ref: session.Ref{Provider: session.ProviderClaude, SessionID: "cl1"}}, "opus-5", "claude --resume cl1 --fork-session --model opus-5"},
		{"opencode", session.Source{Ref: session.Ref{Provider: session.ProviderOpenCode, SessionID: "o1"}}, "", "opencode --session o1 --fork"},
		{"pi path", session.Source{Ref: session.Ref{Provider: session.ProviderPiAgent, SessionID: "p1"}, Path: "/tmp/pi.jsonl"}, "", "pi --fork /tmp/pi.jsonl"},
		{"codex with model", session.Source{Ref: session.Ref{Provider: session.ProviderCodex, SessionID: "c1"}}, "gpt-5.6", "codex fork c1 -m gpt-5.6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, args, err := forkCommand(tt.src, tt.model)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(append([]string{name}, args...), " ")
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunInstallSkillProvider(t *testing.T) {
	dir := t.TempDir()
	skillDirs := map[string]string{session.ProviderCodex: dir}

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"install-skill", "codex"}, session.Roots{}, nil, skillDirs, []byte("# catchup skill\n"), "test", "", nil, &out, &errOut); err != nil {
		t.Fatalf("Run install-skill error: %v (stderr: %s)", err, errOut.String())
	}

	got, err := os.ReadFile(filepath.Join(dir, "catchup", "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}
	if string(got) != "# catchup skill\n" {
		t.Errorf("SKILL.md content = %q, want %q", got, "# catchup skill\n")
	}
	if !strings.Contains(out.String(), filepath.Join(dir, "catchup", "SKILL.md")) {
		t.Errorf("expected installed path in output, got:\n%s", out.String())
	}
}

func TestRunInstallSkillAllProviders(t *testing.T) {
	skillDirs := map[string]string{
		session.ProviderCodex:    t.TempDir(),
		session.ProviderClaude:   t.TempDir(),
		session.ProviderOpenCode: t.TempDir(),
		session.ProviderPiAgent:  t.TempDir(),
	}

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"install-skill"}, session.Roots{}, nil, skillDirs, []byte("content"), "test", "", nil, &out, &errOut); err != nil {
		t.Fatalf("Run install-skill error: %v (stderr: %s)", err, errOut.String())
	}

	for name, dir := range skillDirs {
		if _, err := os.Stat(filepath.Join(dir, "catchup", "SKILL.md")); err != nil {
			t.Errorf("%s: SKILL.md not written: %v", name, err)
		}
	}
}

func withForkRunner(t *testing.T, runner forkRunner) {
	t.Helper()
	old := runFork
	runFork = runner
	t.Cleanup(func() { runFork = old })
}

func withIntoRunner(t *testing.T, runner intoRunner) {
	t.Helper()
	old := runInto
	runInto = runner
	t.Cleanup(func() { runInto = old })
}

func TestRunForkInto(t *testing.T) {
	roots := codexRoot(t)
	var gotName string
	var gotArgs []string
	withIntoRunner(t, func(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		gotName, gotArgs = name, args
		return nil
	})

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"fork", "codex", "--into", "claude"}, roots, nil, nil, nil, "test", "/home/u/src/proj", nil, &out, &errOut); err != nil {
		t.Fatalf("Run fork --into error: %v (stderr: %s)", err, errOut.String())
	}
	if gotName != "claude" {
		t.Fatalf("fork --into launched %q, want claude", gotName)
	}
	if len(gotArgs) != 1 || !strings.Contains(gotArgs[0], "hello from cli test") {
		t.Fatalf("fork --into prompt missing transcript, got %q", gotArgs)
	}
	if !strings.Contains(gotArgs[0], "prior codex session") {
		t.Fatalf("fork --into prompt missing source framing, got %q", gotArgs[0])
	}
}

func TestRunForkIntoSameAgent(t *testing.T) {
	roots := codexRoot(t)
	withIntoRunner(t, func(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		t.Fatal("runner should not be called for a same-agent --into")
		return nil
	})

	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"fork", "codex", "--into", "codex"}, roots, nil, nil, nil, "test", "/home/u/src/proj", nil, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "native fork") {
		t.Fatalf("want same-agent rejection pointing at native fork, got %v", err)
	}
}

func TestIntoCommandModelPlacement(t *testing.T) {
	tests := []struct {
		target string
		want   string // args joined, with PROMPT standing in for the prompt
	}{
		{session.ProviderCodex, "-m M PROMPT"},
		{session.ProviderClaude, "--model M PROMPT"},
		{session.ProviderAgy, "--model M -i PROMPT"},
		{session.ProviderOpenCode, "--model M --prompt PROMPT"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			_, args, err := intoCommand(tt.target, "PROMPT", "M")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(args, " "); got != tt.want {
				t.Fatalf("args %q, want %q (model flag must precede the prompt)", got, tt.want)
			}
		})
	}
}

// codexPairRoot writes two codex sessions sharing one working directory —
// sess-parser is newer, so it is rank 1 — letting the fork selector tests
// prove which session a rank, query, or id picks.
func codexPairRoot(t *testing.T) session.Roots {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "06", "26")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	write := func(id, text string, mod time.Time) {
		body := fmt.Sprintf(`{"timestamp":"2026-06-26T21:31:46.0Z","type":"session_meta","payload":{"id":%q,"cwd":"/home/u/src/proj","cli_version":"0.1"}}
{"timestamp":"2026-06-26T21:31:55.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}}
`, id, text)
		p := filepath.Join(dir, "rollout-"+id+".jsonl")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	write("sess-auth", "fix the auth flow", now.Add(-time.Hour))
	write("sess-parser", "refactor the parser pipeline", now)
	return session.Roots{Codex: root}
}

func TestRunForkSelectors(t *testing.T) {
	roots := codexPairRoot(t)
	cwd := "/home/u/src/proj"

	// fork runs one invocation against a capturing fork runner and reports
	// what (if anything) was dispatched alongside both output streams.
	fork := func(args ...string) (src session.Source, forked bool, out, errOut string, err error) {
		withForkRunner(t, func(ctx context.Context, s session.Source, model string, stdin io.Reader, stdout, stderr io.Writer) error {
			src, forked = s, true
			return nil
		})
		var o, e bytes.Buffer
		err = Run(context.Background(), args, roots, nil, nil, nil, "test", cwd, nil, &o, &e)
		return src, forked, o.String(), e.String(), err
	}

	// A rank indexes the cwd listing, same as a read.
	src, forked, _, _, err := fork("fork", "codex/2")
	if err != nil || !forked || src.Ref.SessionID != "sess-auth" {
		t.Errorf("fork codex/2 = %v %v %v, want sess-auth", src.Ref, forked, err)
	}

	// A unique query hit forks immediately.
	src, forked, _, _, err = fork("fork", "codex", "-q", "auth")
	if err != nil || !forked || src.Ref.SessionID != "sess-auth" {
		t.Errorf("fork codex -q auth = %v %v %v, want sess-auth", src.Ref, forked, err)
	}

	// The query works without an agent name: detection picks the provider,
	// the query picks the session within it.
	src, forked, _, _, err = fork("fork", "-q", "auth")
	if err != nil || !forked || src.Ref.SessionID != "sess-auth" {
		t.Errorf("fork -q auth = %v %v %v, want sess-auth", src.Ref, forked, err)
	}

	// --id resolves the exact session.
	src, forked, _, _, err = fork("fork", "codex", "--id", "sess-parser")
	if err != nil || !forked || src.Ref.SessionID != "sess-parser" {
		t.Errorf("fork codex --id sess-parser = %v %v %v, want sess-parser", src.Ref, forked, err)
	}

	// Several query hits fork nothing: the listing plus a rerun hint is the
	// answer, and the exit is clean.
	_, forked, out, errOut, err := fork("fork", "codex", "-q", "the")
	if err != nil {
		t.Fatalf("ambiguous fork query errored: %v", err)
	}
	if forked {
		t.Error("ambiguous fork query must not fork")
	}
	for _, want := range []string{"sess-auth", "sess-parser"} {
		if !strings.Contains(out, want) {
			t.Errorf("ambiguous fork listing missing %s:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "rerun as catchup fork codex/<rank>") {
		t.Errorf("ambiguous fork hint missing:\n%s", errOut)
	}

	// A query matching nothing is an error, not a silent no-op.
	if _, _, _, _, err := fork("fork", "codex", "-q", "nonexistent-topic"); err == nil {
		t.Error("fork with a no-hit query should error")
	}
}

// codexBigRoot writes one codex session whose user message is an oversized
// paste: prose at both edges, a repetitive log blob in the middle.
func codexBigRoot(t *testing.T) session.Roots {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "06", "26")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := "INTRO before the blob\n" +
		strings.Repeat("2026-06-26T21:00:00 GET /health 200 17ms\n", 200) +
		"OUTRO after the blob"
	text, err := json.Marshal(big)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"timestamp":"2026-06-26T21:31:46.0Z","type":"session_meta","payload":{"id":"sess-big","cwd":"/home/u/src/proj","cli_version":"0.1"}}
{"timestamp":"2026-06-26T21:31:55.0Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%s}]}}
`, text)
	if err := os.WriteFile(filepath.Join(dir, "rollout-sess-big.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return session.Roots{Codex: root}
}

func TestRunClampsOversizedEntries(t *testing.T) {
	roots := codexBigRoot(t)
	cwd := "/home/u/src/proj"

	// Default render: edges survive, the blob is elided behind a marker.
	out := runWithCwd(t, roots, cwd, "codex")
	for _, want := range []string{"INTRO before the blob", "OUTRO after the blob", "elided; rerun with --full"} {
		if !strings.Contains(out, want) {
			t.Errorf("clamped render missing %q:\n%.400s", want, out)
		}
	}
	if n := strings.Count(out, "GET /health"); n >= 200 {
		t.Errorf("clamped render kept all %d blob lines", n)
	}

	// --full restores the whole entry.
	full := runWithCwd(t, roots, cwd, "codex", "--full")
	if strings.Contains(full, "elided") {
		t.Error("--full still clamped")
	}
	if n := strings.Count(full, "GET /health"); n != 200 {
		t.Errorf("--full kept %d blob lines, want 200", n)
	}

	// --json stays faithful without any flag.
	js := runWithCwd(t, roots, cwd, "codex", "--json")
	if strings.Contains(js, "elided") {
		t.Error("--json output was clamped")
	}

	// fork --into seeds the clamped transcript; --full seeds it whole.
	var prompt string
	withIntoRunner(t, func(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		prompt = args[len(args)-1]
		return nil
	})
	var o, e bytes.Buffer
	if err := Run(context.Background(), []string{"fork", "codex", "--into", "claude"}, roots, nil, nil, nil, "test", cwd, nil, &o, &e); err != nil {
		t.Fatalf("fork --into error: %v (stderr: %s)", err, e.String())
	}
	if !strings.Contains(prompt, "elided; rerun with --full") {
		t.Error("seeded transcript was not clamped")
	}
	if err := Run(context.Background(), []string{"fork", "codex", "--into", "claude", "--full"}, roots, nil, nil, nil, "test", cwd, nil, &o, &e); err != nil {
		t.Fatalf("fork --into --full error: %v (stderr: %s)", err, e.String())
	}
	if strings.Contains(prompt, "elided") {
		t.Error("--full seed was still clamped")
	}
}

func withTTY(t *testing.T, open func() (io.ReadCloser, error)) {
	t.Helper()
	old := openTTY
	openTTY = open
	t.Cleanup(func() { openTTY = old })
}

// forkFromRun launches one fork --from invocation against a capturing into
// runner, with no provider roots at all — proving the artifact path never
// touches a session store.
func forkFromRun(t *testing.T, stdin io.Reader, args ...string) (name string, prompt string, childStdin io.Reader, err error) {
	t.Helper()
	var gotArgs []string
	withIntoRunner(t, func(ctx context.Context, n string, a []string, in io.Reader, stdout, stderr io.Writer) error {
		name, gotArgs, childStdin = n, a, in
		return nil
	})
	var out, errOut bytes.Buffer
	err = Run(context.Background(), args, session.Roots{}, nil, nil, nil, "test", "", stdin, &out, &errOut)
	if len(gotArgs) > 0 {
		prompt = gotArgs[len(gotArgs)-1]
	}
	return name, prompt, childStdin, err
}

func TestRunForkFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoff.md")
	if err := os.WriteFile(path, []byte("# prior session\ncarry on with the parser"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, prompt, _, err := forkFromRun(t, nil, "fork", "--into", "claude", "--from", path)
	if err != nil {
		t.Fatalf("fork --from file error: %v", err)
	}
	if name != "claude" {
		t.Fatalf("launched %q, want claude", name)
	}
	for _, want := range []string{"carry on with the parser", "Source: " + path, "transcript or handoff notes"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("seed prompt missing %q:\n%.300s", want, prompt)
		}
	}
}

func TestRunForkFromStdin(t *testing.T) {
	fakeTTY := io.NopCloser(strings.NewReader("user typing"))
	withTTY(t, func() (io.ReadCloser, error) { return fakeTTY, nil })

	pipe := strings.NewReader("piped transcript body")
	_, prompt, childStdin, err := forkFromRun(t, pipe, "fork", "--into", "codex", "--from", "-")
	if err != nil {
		t.Fatalf("fork --from - error: %v", err)
	}
	if !strings.Contains(prompt, "piped transcript body") || !strings.Contains(prompt, "Source: stdin") {
		t.Errorf("seed prompt wrong:\n%.300s", prompt)
	}
	if childStdin != io.Reader(fakeTTY) {
		t.Error("launched agent must get the reopened terminal, not the exhausted pipe")
	}
}

func TestRunForkFromStdinNoTTY(t *testing.T) {
	withTTY(t, func() (io.ReadCloser, error) { return nil, fmt.Errorf("no controlling terminal") })
	_, _, _, err := forkFromRun(t, strings.NewReader("body"), "fork", "--into", "codex", "--from", "-")
	if err == nil || !strings.Contains(err.Error(), "--from <path>") {
		t.Fatalf("want no-terminal error navigating to a file, got %v", err)
	}
}

func TestRunForkFromURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "remote transcript body")
	}))
	defer srv.Close()

	// The query string stands in for presigned auth: it must reach the
	// server but never the seeded prompt, which persists in the receiving
	// agent's own session store.
	_, prompt, _, err := forkFromRun(t, nil, "fork", "--into", "claude", "--from", srv.URL+"/s.md?X-Sig=secret")
	if err != nil {
		t.Fatalf("fork --from url error: %v", err)
	}
	if !strings.Contains(prompt, "remote transcript body") || !strings.Contains(prompt, "Source: "+srv.URL+"/s.md") {
		t.Errorf("seed prompt wrong:\n%.300s", prompt)
	}
	if strings.Contains(prompt, "secret") {
		t.Error("presigned query leaked into the seeded prompt")
	}

	bad := httptest.NewServer(http.NotFoundHandler())
	defer bad.Close()
	if _, _, _, err := forkFromRun(t, nil, "fork", "--into", "claude", "--from", bad.URL); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want status error for non-200, got %v", err)
	}
}

func TestRunForkFromEmptyArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(path, []byte("  \n\t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := forkFromRun(t, nil, "fork", "--into", "claude", "--from", path)
	if err == nil || !strings.Contains(err.Error(), "nothing to seed") {
		t.Fatalf("want empty-artifact error, got %v", err)
	}
}

func TestRunForkFromUnknownAgent(t *testing.T) {
	_, _, _, err := forkFromRun(t, nil, "fork", "--into", "bogus", "--from", "s.md")
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("want unknown agent error before any read, got %v", err)
	}
}

func TestSeedIntoSizeGates(t *testing.T) {
	ctx := context.Background()
	var runErr error
	withIntoRunner(t, func(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		return runErr
	})

	// At the warning threshold: silent.
	var quiet bytes.Buffer
	if err := seedInto(ctx, "claude", "", strings.Repeat("a", warnTranscriptBytes), "HINT", nil, nil, &quiet); err != nil {
		t.Fatal(err)
	}
	if quiet.Len() != 0 {
		t.Errorf("warned at threshold: %q", quiet.String())
	}

	// Above it: warns with the caller's own trim hint, still launches.
	var warned bytes.Buffer
	if err := seedInto(ctx, "claude", "", strings.Repeat("a", warnTranscriptBytes+1), "HINT", nil, nil, &warned); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warned.String(), "HINT") {
		t.Errorf("warning must carry the caller's trim hint, got %q", warned.String())
	}

	// No size is pre-rejected — the OS owns the argv ceiling — but exec's
	// terse E2BIG refusal is translated into the same trim navigation.
	runErr = fmt.Errorf("fork/exec /usr/bin/claude: %w", syscall.E2BIG)
	err := seedInto(ctx, "claude", "", "small prompt", "HINT", nil, nil, &quiet)
	if err == nil || !strings.Contains(err.Error(), "argument limit") || !strings.Contains(err.Error(), "HINT") {
		t.Fatalf("want E2BIG translated with the trim hint, got %v", err)
	}

	// Every other launch failure passes through untouched.
	runErr = fmt.Errorf("exec: %q: executable file not found", "claude")
	if err := seedInto(ctx, "claude", "", "small prompt", "HINT", nil, nil, &quiet); err == nil || strings.Contains(err.Error(), "HINT") {
		t.Fatalf("non-E2BIG failure must pass through untranslated, got %v", err)
	}
}

// When the OS refuses an oversized --from seed, the navigation must name the
// artifact-side trim, never the --last/--since-compact flags --from rejects.
func TestRunForkFromOversizedArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.md")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 200*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	withIntoRunner(t, func(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		return fmt.Errorf("fork/exec /usr/bin/claude: %w", syscall.E2BIG)
	})
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"fork", "--into", "claude", "--from", path}, session.Roots{}, nil, nil, nil, "test", "", nil, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "catchup <agent> --last 20 > s.md") {
		t.Fatalf("want artifact-side trim navigation, got %v", err)
	}
	if strings.Contains(err.Error(), "rerun with --last") {
		t.Errorf("hint names flags --from rejects: %v", err)
	}
}
