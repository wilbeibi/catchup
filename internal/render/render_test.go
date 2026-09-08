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

// TestFailureViews pins the audience split: human Markdown and HTML omit tool
// failures, agent Markdown frames them as quoted records, and JSON keeps the
// structured call input.
func TestFailureViews(t *testing.T) {
	ts := time.Date(2026, 9, 2, 4, 11, 45, 0, time.UTC)
	th := sampleThread()
	th.Entries = append(th.Entries, session.Failure(
		"Bash", json.RawMessage(`{"command":"go test ./..."}`),
		"FAIL\tcatchup/x <0.1s>\n```\nexit status 1", ts,
	))

	var b bytes.Buffer
	if err := Thread(&b, th, session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "failure: Bash") || strings.Contains(b.String(), "FAIL\tcatchup") {
		t.Errorf("human markdown exposed a tool failure:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "entries: 3\n") {
		t.Errorf("human markdown counted a hidden failure:\n%s", b.String())
	}

	// A keyword read is the exception to the clean human projection: the
	// failure may be the passage the user explicitly searched for, so removing
	// it would return a result with no visible match.
	b.Reset()
	queried := th
	queried.Entries = append(queried.Entries, session.Failure("webfetch", nil, "unrelated 404", ts))
	queried.Excerpt = `"exit status 1" matched 1 entry; source entries 4-5 of 5`
	queried.Query = "exit status 1"
	if err := Thread(&b, queried, session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "FAIL\tcatchup") ||
		!strings.Contains(b.String(), `\"exit status 1\" matched 1 entry; source entries 4-5 of 5`) ||
		strings.Contains(b.String(), "unrelated 404") {
		t.Errorf("queried markdown did not isolate the matching failure:\n%s", b.String())
	}

	b.Reset()
	if err := Thread(&b, th, session.FormatAgent); err != nil {
		t.Fatal(err)
	}
	wantAgent := []string{
		"entries: 4\n",
		"## 4. failure: Bash | 2026-09-02 04:11",
		"### Input\n\n```text\n{\"command\":\"go test ./...\"}\n```",
		"### Output\n\n````text\nFAIL\tcatchup/x <0.1s>\n```\nexit status 1\n````",
	}
	for _, want := range wantAgent {
		if !strings.Contains(b.String(), want) {
			t.Errorf("agent markdown missing %q:\n%s", want, b.String())
		}
	}

	// A provider that records no call input leaves out the heading rather
	// than framing an empty block.
	b.Reset()
	bare := sampleThread()
	bare.Entries = append(bare.Entries, session.Failure("webfetch", nil, "404", ts))
	if err := Thread(&b, bare, session.FormatAgent); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "### Input") || !strings.Contains(b.String(), "### Output\n\n```text\n404\n```") {
		t.Errorf("inputless failure not framed as output only:\n%s", b.String())
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
	input, ok := f["input"].(map[string]any)
	if f["kind"] != "failure" || f["role"] != "tool" || f["tool"] != "Bash" || !ok || input["command"] != "go test ./..." {
		t.Errorf("failure entry = %v", f)
	}

	b.Reset()
	if err := Thread(&b, th, session.FormatHTML); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "failure: Bash") || strings.Contains(b.String(), "FAIL\tcatchup") {
		t.Errorf("human html exposed a tool failure:\n%s", b.String())
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

// A queried listing answers "why this row" in the column an unqueried one spends
// on the title, and says so in the header.
func TestListShowsTheMatch(t *testing.T) {
	sums := []session.Summary{
		{Ref: session.Ref{Provider: "codex", SessionID: "a"}, Rank: 1, UpdatedAt: time.Now(),
			Title: "catchup", Cwd: "/src/catchup", Preview: "opening message",
			Match: &session.Match{Role: session.RoleAssistant, Kind: session.KindMessage,
				Text: "the egress rule\nwas wrong", TruncatedBefore: true, TruncatedAfter: true}},
	}
	var b bytes.Buffer
	if err := List(&b, "codex", sums, session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "MATCH") || strings.Contains(out, "TITLE") {
		t.Errorf("queried listing should head the column MATCH, not TITLE:\n%s", out)
	}
	// One row stays one line: the passage's own newlines are the table's problem.
	if !strings.Contains(out, "the egress rule was wrong") {
		t.Errorf("match not collapsed onto the row:\n%s", out)
	}
	if n := strings.Count(strings.TrimSpace(out), "\n"); n != 1 {
		t.Errorf("listing is %d lines past the header, want 1:\n%s", n, out)
	}
	if !strings.Contains(out, "…the egress") || !strings.Contains(out, "wrong…") {
		t.Errorf("a windowed passage should be elided on both sides:\n%s", out)
	}
	if strings.Contains(out, "catchup ") && strings.Contains(out, "opening message") {
		t.Errorf("title or preview leaked into a queried row:\n%s", out)
	}
}

// JSON has no single column to spend, so it keeps the title and adds the match.
func TestJSONListCarriesMatchAndTitle(t *testing.T) {
	sums := []session.Summary{
		{Ref: session.Ref{Provider: "codex", SessionID: "a"}, Rank: 1, UpdatedAt: time.Now(),
			Title: "catchup", Preview: "opening message",
			Match: &session.Match{Role: session.RoleUser, Kind: session.KindMessage,
				Text: "the egress rule\nwas wrong", TruncatedAfter: true}},
	}
	var b bytes.Buffer
	if err := List(&b, "codex", sums, session.FormatJSON); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`"title": "catchup"`,
		`"role": "user"`,
		`"kind": "message"`,
		`"the egress rule\nwas wrong"`, // newlines survive for the agent reading this
		`"truncated_after": true`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("json listing missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "truncated_before") {
		t.Errorf("an untruncated side should be omitted, not spelled false:\n%s", out)
	}
	// An unqueried listing carries no match key at all.
	b.Reset()
	if err := List(&b, "codex", []session.Summary{{Ref: sums[0].Ref, Rank: 1, Title: "catchup"}}, session.FormatJSON); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "match") {
		t.Errorf("unqueried json listing carries a match key:\n%s", b.String())
	}
}
