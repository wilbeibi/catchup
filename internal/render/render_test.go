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

func TestList(t *testing.T) {
	var b bytes.Buffer
	sums := []session.Summary{
		{Ref: session.Ref{Provider: "codex", SessionID: "019f05d8"}, Rank: 1,
			UpdatedAt: time.Now(), Title: "skeleton", Cwd: "/src/catchup", Preview: "let's\nimplement"},
	}
	if err := List(&b, "codex", sums); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "#") || !strings.Contains(out, "019f05d8") {
		t.Errorf("list missing header or row:\n%s", out)
	}
	if strings.Contains(out, "let's") {
		t.Errorf("preview should not appear in list:\n%s", out)
	}
	// Full session IDs are preserved so --id can restore them.
	sums[0].Ref.SessionID = "deadbeef-cafe-babe-0123-456789abcdef"
	b.Reset()
	if err := List(&b, "codex", sums); err != nil {
		t.Fatal(err)
	}
	out = b.String()
	if !strings.Contains(out, "deadbeef-cafe-babe-0123-456789abcdef") {
		t.Errorf("full session id should appear:\n%s", out)
	}
}

// TestListCJKAlignment locks in the display-width-aware padding: a CJK title
// (2 columns per rune) must not shift the SESSION column relative to the
// header or to an ASCII-only row. Regression for the tabwriter replacement.
func TestListCJKAlignment(t *testing.T) {
	// termWidth falls back to $COLUMNS when w is not a *os.File.
	t.Setenv("COLUMNS", "80")

	cases := []struct {
		name  string
		title string
	}{
		{"ascii", "Engineering basics"},
		{"cjk", "Engineering博文三结论开头写法"},
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
	if err := List(&b, "codex", sums); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != len(cases)+1 {
		t.Fatalf("expected %d lines, got %d:\n%s", len(cases)+1, len(lines), b.String())
	}

	// The SESSION column must start at the same *display column* in every
	// line. CJK runes are 3 bytes but 2 columns, so byte offset is not
	// enough — measure the display width of the prefix before SESSION.
	marker := "0123456789abcdef"
	want := runewidth.StringWidth(lines[0][:strings.Index(lines[0], "SESSION")])
	if want < 0 {
		t.Fatalf("header missing SESSION:\n%s", lines[0])
	}
	for i, ln := range lines[1:] {
		idx := strings.Index(ln, marker)
		if idx < 0 {
			t.Fatalf("line %d missing session id:\n%s", i+1, ln)
		}
		got := runewidth.StringWidth(ln[:idx])
		if got != want {
			t.Errorf("line %d (%s): SESSION at display col %d, want %d (header)\n%s",
				i+1, cases[i].name, got, want, ln)
		}
	}
}
