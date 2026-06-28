package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
		"provider: codex\n",
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
	if doc.Provider != "codex" || doc.SessionID != "019f05d8" {
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
	if !strings.Contains(out, "RANK") || !strings.Contains(out, "019f05d8") {
		t.Errorf("list missing header or row:\n%s", out)
	}
	if strings.Contains(out, "let's\nimplement") {
		t.Errorf("preview newline should be collapsed:\n%s", out)
	}
}
