package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/wilbeibi/catchup/internal/session"
)

func sampleThread() session.Thread {
	ts := time.Date(2026, 6, 26, 14, 31, 0, 0, time.UTC)
	return session.Thread{
		Source: session.Source{
			Ref:       session.Ref{Provider: "codex", SessionID: "019f05d8"},
			Path:      "/home/u/.codex/sessions/x.jsonl",
			UpdatedAt: ts,
			Metadata:  map[string]string{"title": "catchup: skeleton", "cwd": "/home/u/src/catchup"},
		},
		Entries: []session.Entry{
			{Kind: session.KindMessage, Role: session.RoleUser, Text: "hello <there>", Time: ts},
			{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "hi & welcome", Time: ts},
			{Kind: session.KindCompact, Text: ""},
		},
	}
}

func TestMarkdownThread(t *testing.T) {
	var b bytes.Buffer
	if err := Thread(&b, sampleThread(), session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	// Frontmatter: title contains a colon, so it must be quoted.
	for _, want := range []string{
		"---\n",
		"agent: codex\n",
		"session: 019f05d8\n",
		`title: "catchup: skeleton"` + "\n",
		"entries: 3\n",
		"## 1. user | 2026-06-26 14:31",
		"## 2. assistant",
		"## 3. compact",
		"_(context compacted)_",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, out)
		}
	}
}

func TestJSONThreadShape(t *testing.T) {
	var b bytes.Buffer
	if err := Thread(&b, sampleThread(), session.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var doc threadDoc
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if doc.Agent != "codex" || doc.SessionID != "019f05d8" {
		t.Errorf("bad source doc: %+v", doc.sourceDoc)
	}
	if len(doc.Entries) != 3 || doc.Entries[0].Index != 1 || doc.Entries[0].Role != "user" {
		t.Errorf("bad entries: %+v", doc.Entries)
	}
	// Raw text must be preserved, not HTML-escaped.
	if !strings.Contains(b.String(), "hi & welcome") {
		t.Errorf("expected unescaped text in JSON:\n%s", b.String())
	}
}

func TestHTMLEscapes(t *testing.T) {
	var b bytes.Buffer
	if err := Thread(&b, sampleThread(), session.FormatHTML); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "hello <there>") {
		t.Errorf("html did not escape user text:\n%s", out)
	}
	if !strings.Contains(out, "hello &lt;there&gt;") {
		t.Errorf("expected escaped user text in html:\n%s", out)
	}
}

// TestFailureEntry pins the one shape a failure takes in every format: the
// heading names the tool, the input precedes the output, and JSON carries
// tool/input only on failures.
func TestFailureEntry(t *testing.T) {
	ts := time.Date(2026, 9, 2, 4, 11, 45, 0, time.UTC)
	th := sampleThread()
	th.Entries = append(th.Entries, session.Failure("Bash", "go test ./...", "FAIL\tcatchup/x <0.1s>\nexit status 1", ts))

	var b bytes.Buffer
	if err := Thread(&b, th, session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	wantMD := "## 4. failure: Bash | 2026-09-02 04:11\n\ngo test ./...\n\nFAIL\tcatchup/x <0.1s>\nexit status 1\n"
	if !strings.Contains(b.String(), wantMD) {
		t.Errorf("markdown failure block missing %q:\n%s", wantMD, b.String())
	}

	b.Reset()
	if err := Thread(&b, th, session.FormatJSON); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if _, has := doc.Entries[0]["tool"]; has {
		t.Errorf("message entry carries tool: %v", doc.Entries[0])
	}
	f := doc.Entries[3]
	if f["kind"] != "failure" || f["role"] != "tool" || f["tool"] != "Bash" || f["input"] != "go test ./..." {
		t.Errorf("failure entry = %v", f)
	}

	b.Reset()
	if err := Thread(&b, th, session.FormatHTML); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`class="entry role-tool kind-failure"`, "4. failure: Bash", `<pre class="input">go test ./...</pre>`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("html missing %q:\n%s", want, b.String())
		}
	}
}

func TestList(t *testing.T) {
	var b bytes.Buffer
	sums := []session.Summary{
		{Ref: session.Ref{Provider: "codex", SessionID: "deadbeef-cafe-babe-0123-456789abcdef"}, Rank: 3,
			UpdatedAt: time.Now(), Title: "skeleton", Cwd: "/src/catchup", Preview: "let's\nimplement"},
	}
	if err := List(&b, "codex", sums, session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	// The row's handle is the command that re-selects it.
	if !strings.Contains(out, "SESSION") || !strings.Contains(out, "codex/3") {
		t.Errorf("list missing header or handle:\n%s", out)
	}
	// The id is --json's business; in the table it only steals title width.
	if strings.Contains(out, "deadbeef") {
		t.Errorf("session id should not appear in the human table:\n%s", out)
	}
	if strings.Contains(out, "let's") {
		t.Errorf("preview should not appear in list:\n%s", out)
	}
}

// TestListCrossAgent covers the bare `catchup --list` table: no listing-wide
// provider, so every row must label itself.
func TestListCrossAgent(t *testing.T) {
	sums := []session.Summary{
		{Ref: session.Ref{Provider: "claude", SessionID: "a"}, Rank: 1, UpdatedAt: time.Now(), Title: "one"},
		{Ref: session.Ref{Provider: "codex", SessionID: "b"}, Rank: 2, UpdatedAt: time.Now(), Title: "two"},
	}
	var b bytes.Buffer
	if err := List(&b, "", sums, session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"claude/1", "codex/2"} {
		if !strings.Contains(out, want) {
			t.Errorf("cross-agent listing missing %q:\n%s", want, out)
		}
	}

	b.Reset()
	if err := List(&b, "", nil, session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != "no sessions found\n" {
		t.Errorf("empty cross-agent listing = %q", got)
	}
}

func TestAge(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = time.Now })

	cases := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{14 * time.Minute, "14m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
		{6 * 24 * time.Hour, "6d ago"},
		// Past a week, "how long ago" stops being the question.
		{9 * 24 * time.Hour, now.Add(-9 * 24 * time.Hour).Local().Format(dateHuman)},
	}
	for _, c := range cases {
		if got := Age(now.Add(-c.ago)); got != c.want {
			t.Errorf("Age(-%s) = %q, want %q", c.ago, got, c.want)
		}
	}
	if got := Age(time.Time{}); got != "" {
		t.Errorf("Age(zero) = %q, want empty", got)
	}
}

// TestListCJKAlignment locks in the display-width-aware layout: a CJK title
// (2 columns per rune) must not shift the TITLE column relative to the header
// or to an ASCII-only row, and must not overflow the terminal width.
func TestListCJKAlignment(t *testing.T) {
	// termWidth falls back to $COLUMNS when w is not a *os.File.
	t.Setenv("COLUMNS", "80")

	cases := []struct {
		name  string
		title string
	}{
		{"ascii", "Engineering basics"},
		{"cjk", "Engineering博文三结论开头写法博文三结论开头写法博文三结论开头写法博文三结论开头写法"},
	}
	sums := make([]session.Summary, 0, len(cases))
	for i, c := range cases {
		sums = append(sums, session.Summary{
			Ref:       session.Ref{Provider: "codex", SessionID: "0123456789abcdef"},
			Rank:      i + 1,
			UpdatedAt: time.Date(2026, 6, 28, 14, 9, 0, 0, time.UTC),
			Title:     c.title,
		})
	}

	var b bytes.Buffer
	if err := List(&b, "codex", sums, session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != len(cases)+1 {
		t.Fatalf("expected %d lines, got %d:\n%s", len(cases)+1, len(lines), b.String())
	}

	// The TITLE column must start at the same *display column* in every line.
	// CJK runes are 3 bytes but 2 columns, so byte offset is not enough —
	// measure the display width of the prefix before the title.
	want := runewidth.StringWidth(lines[0][:strings.Index(lines[0], "TITLE")])
	for i, ln := range lines[1:] {
		idx := strings.Index(ln, "Engineering")
		if idx < 0 {
			t.Fatalf("line %d missing title:\n%s", i+1, ln)
		}
		if got := runewidth.StringWidth(ln[:idx]); got != want {
			t.Errorf("line %d (%s): TITLE at display col %d, want %d (header)\n%s",
				i+1, cases[i].name, got, want, ln)
		}
		if got := runewidth.StringWidth(ln); got > 80 {
			t.Errorf("line %d (%s): %d display columns, want <= 80\n%s", i+1, cases[i].name, got, ln)
		}
	}
}
