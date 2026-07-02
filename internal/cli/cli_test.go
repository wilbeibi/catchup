package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	if err := Run(context.Background(), args, roots, nil, nil, nil, cwd, nil, &out, &errOut); err != nil {
		t.Fatalf("Run(%v) error: %v (stderr: %s)", args, err, errOut.String())
	}
	return out.String()
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
	if err := Run(context.Background(), []string{"claude"}, roots, current, nil, nil, cwd, nil, &got, &errOut); err != nil {
		t.Fatalf("Run error: %v (stderr: %s)", err, errOut.String())
	}
	if !strings.Contains(got.String(), "session: sess-old") {
		t.Errorf("injected current id should win over newest, got:\n%s", got.String())
	}
	if strings.Contains(got.String(), "sess-new") {
		t.Errorf("newest session leaked despite injected current id:\n%s", got.String())
	}
}

func TestRunUnknownProvider(t *testing.T) {
	var out, errOut bytes.Buffer
	err := Run(context.Background(), []string{"bogus"}, session.Roots{}, nil, nil, nil, "", nil, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("expected unknown agent error, got %v", err)
	}
}

func TestRunForkProvider(t *testing.T) {
	roots := codexRoot(t)
	var got session.Source
	withForkRunner(t, func(ctx context.Context, src session.Source, stdin io.Reader, stdout, stderr io.Writer) error {
		got = src
		return nil
	})

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"fork", "codex"}, roots, nil, nil, nil, "/home/u/src/proj", nil, &out, &errOut); err != nil {
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
	withForkRunner(t, func(ctx context.Context, src session.Source, stdin io.Reader, stdout, stderr io.Writer) error {
		got = src
		return nil
	})

	var out, errOut bytes.Buffer
	if err := Run(context.Background(), []string{"fork"}, roots, nil, nil, nil, "/home/u/src/proj", nil, &out, &errOut); err != nil {
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
		name string
		src  session.Source
		want string
	}{
		{"codex", session.Source{Ref: session.Ref{Provider: session.ProviderCodex, SessionID: "c1"}}, "codex fork c1"},
		{"claude", session.Source{Ref: session.Ref{Provider: session.ProviderClaude, SessionID: "cl1"}}, "claude --resume cl1 --fork-session"},
		{"opencode", session.Source{Ref: session.Ref{Provider: session.ProviderOpenCode, SessionID: "o1"}}, "opencode --session o1 --fork"},
		{"pi path", session.Source{Ref: session.Ref{Provider: session.ProviderPiAgent, SessionID: "p1"}, Path: "/tmp/pi.jsonl"}, "pi --fork /tmp/pi.jsonl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, args, err := forkCommand(tt.src)
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
	if err := Run(context.Background(), []string{"install-skill", "codex"}, session.Roots{}, nil, skillDirs, []byte("# catchup skill\n"), "", nil, &out, &errOut); err != nil {
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
	if err := Run(context.Background(), []string{"install-skill"}, session.Roots{}, nil, skillDirs, []byte("content"), "", nil, &out, &errOut); err != nil {
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
