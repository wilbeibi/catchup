package render

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wilbeibi/catchup/internal/session"
)

func TestStripControl(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "nothing to strip", "nothing to strip"},
		{"empty", "", ""},
		{"newline and tab kept", "a\tb\nc\n", "a\tb\nc\n"},

		{"csi colour", "a \x1b[31mred\x1b[0m b", "a red b"},
		{"csi at start", "\x1b[2Jcleared", "cleared"},
		{"csi at end", "line\x1b[K", "line"},
		{"csi back to back", "\x1b[1m\x1b[4m\x1b[31mx", "x"},
		{"csi private param", "\x1b[?25lhidden", "hidden"},
		{"csi intermediate", "\x1b[4 qbar cursor", "bar cursor"},
		{"csi truncated at end", "text\x1b[38;5;", "text"},
		{"csi aborted by esc", "\x1b[38\x1b[0mz", "z"},
		{"csi around multibyte", "中\x1b[0m文", "中文"},

		{"osc bel", "a\x1b]0;window title\x07b", "ab"},
		{"osc st", "a\x1b]52;c;ZXZpbA==\x1b\\b", "ab"},
		{"osc c1 st", "a\x1b]0;title\u009cb", "ab"},
		{"osc empty body", "a\x1b]\x07b", "ab"},
		{"osc unterminated keeps payload", "a\x1b]0;no terminator", "a0;no terminator"},
		{"osc unterminated stops at newline", "a\x1b]0;title\nkept", "a0;title\nkept"},
		{"osc long", "a\x1b]52;c;" + strings.Repeat("QQ", 50000) + "\x07b", "ab"},
		{"dcs", "a\x1bPq#0;2;0;0;0\x1b\\b", "ab"},
		{"apc", "a\x1b_notify\x1b\\b", "ab"},
		{"pm", "a\x1b^private\x1b\\b", "ab"},
		{"sos", "a\x1bXstring\x1b\\b", "ab"},

		{"two byte esc", "a\x1bcb", "ab"},           // ESC c, full reset
		{"esc with intermediate", "a\x1b(Bb", "ab"}, // charset designation
		{"esc alone at end", "ab\x1b", "ab"},
		{"esc before multibyte rune", "a\x1b世b", "a世b"},
		{"esc esc csi", "\x1b\x1b[31mx", "x"},

		{"c0 controls", "a\x00b\x07c\x08d\x0be\x0cf", "abcdef"},
		{"bare cr", "typed\rreplaced", "typedreplaced"},
		{"crlf keeps the newline", "a\r\nb", "a\nb"},
		{"del", "a\x7fb", "ab"},
		{"c1 control", "a\u0085b", "ab"},
		{"c1 csi", "x\u009b31mred", "xred"},
		{"c1 osc", "x\u009d0;title\x07y", "xy"},

		// Nothing legitimate may be lost: the continuation bytes of these runes
		// fall in 0x80-0x9F, which a byte-level filter would eat.
		{"chinese", "中文标题：修复渲染", "中文标题：修复渲染"},
		{"korean", "한국어 세션 제목", "한국어 세션 제목"},
		{"emoji", "ship it 🚀👍🏽 done", "ship it 🚀👍🏽 done"},
		{"zwj emoji", "👨‍👩‍👧‍👦", "👨‍👩‍👧‍👦"},
		{"combining", "café e\u0301\u0301 ǹ", "café e\u0301\u0301 ǹ"},
		{"latin1 punctuation", "© ® ° ± « » 120 µs", "© ® ° ± « » 120 µs"},
		{"cjk with escapes", "\x1b[32m中文\x1b[0m한국어🚀", "中文한국어🚀"},

		{"invalid utf8 kept when clean", "a\xffb", "a\xffb"},
		{"invalid utf8 dropped when stripping", "\xff\xfe\x1b[31mok", "ok"},
		{"truncated lead byte", "ok\x00\xc2", "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := StripControl(c.in); got != c.want {
				t.Errorf("StripControl(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// sink defeats the compiler's dead-store elimination in the allocation test.
var sink string

// TestStripControlKeepsCleanTextFree pins the fast path: a listing runs
// StripControl over every row of every session it walks, so text with nothing
// to strip must cost a scan and no copy.
func TestStripControlKeepsCleanTextFree(t *testing.T) {
	for _, s := range []string{
		"catchup: render the listing",
		"中文标题 with café ©, 🚀 and\ttabs\nand newlines",
	} {
		if n := testing.AllocsPerRun(100, func() { sink = StripControl(s) }); n != 0 {
			t.Errorf("StripControl(%q) allocated %v times, want 0", s, n)
		}
	}
}

func FuzzStripControl(f *testing.F) {
	for _, s := range []string{
		"", "plain", "a\x1b[31mb", "\x1b]0;t\x07", "\x1b]0;unterminated", "\x1b",
		"\u009b31m", "中文\x07한국어", "👍🏽\x1b\\", "\xff\xc2\x00\x9b", "a\r\nb",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := StripControl(s)
		if len(got) > len(s) {
			t.Fatalf("StripControl(%q) = %q, longer than its input", s, got)
		}
		if utf8.ValidString(s) && !utf8.ValidString(got) {
			t.Fatalf("StripControl(%q) = %q, not valid UTF-8", s, got)
		}
		for _, r := range got {
			if r == escByte || r == delRune || (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x80 && r <= 0x9f) {
				t.Fatalf("StripControl(%q) = %q, kept control %U", s, got, r)
			}
		}
		if again := StripControl(got); again != got {
			t.Fatalf("StripControl is not idempotent: %q -> %q -> %q", s, got, again)
		}
	})
}

// escapedThread is the end-to-end fixture: a session whose every displayed
// string carries a sequence a provider would have copied out of tool output.
func escapedThread() session.Thread {
	ts := time.Date(2026, 9, 18, 9, 30, 0, 0, time.UTC)
	return session.Thread{
		Source: session.Source{
			Ref:       session.Ref{Provider: "codex", SessionID: "019f05d8\x1b[0m"},
			Path:      "/home/u/.codex/sessions/x.jsonl",
			UpdatedAt: ts,
			Metadata: map[string]string{
				"title": "fix \x1b]0;pwned\x07the parser",
				"cwd":   "/home/u/src/catchup",
			},
			Warnings: []string{"truncated record\x1b[31m"},
		},
		Entries: []session.Entry{
			{Kind: session.KindMessage, Role: session.RoleUser, Text: "why does \x1b[1mthe parser\x1b[0m fail 中文?", Time: ts},
			{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "because of \x1b]52;c;ZXZpbA==\x07the header", Time: ts},
			session.Failure("Ba\x1b[31msh", []byte(`{"command":"echo \u001b[31m"}`), "exit \x1b[7mstatus\x1b[0m 1", ts),
		},
	}
}

// TestRenderStripsControlSequences walks the real render paths: no ESC may
// reach the reader in any displayed mode, while the words around it stay.
func TestRenderStripsControlSequences(t *testing.T) {
	th := escapedThread()
	th.Excerpt = "\"parser\" matched 1 entry\x1b[0m; source entries 1-2 of 3"
	th.Query = "parser"

	for _, f := range []session.Format{session.FormatMarkdown, session.FormatAgent, session.FormatHTML} {
		var b bytes.Buffer
		if err := Thread(&b, th, f); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out := b.String()
		assertNoControls(t, f.String(), out)
		for _, want := range []string{"fix the parser", "why does the parser fail 中文?", "because of the header"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: lost visible text %q:\n%s", f, want, out)
			}
		}
	}

	// Agent Markdown is the one view that shows a failure, including the call
	// input, which is decoded from JSON and so can hide an escape of its own.
	var b bytes.Buffer
	if err := Thread(&b, th, session.FormatAgent); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"failure: Bash", "exit status 1", `{"command":"echo \u001b[31m"}`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("agent markdown missing %q:\n%s", want, b.String())
		}
	}

	b.Reset()
	if err := Meta(&b, escapedThread().Source, session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	assertNoControls(t, "meta", b.String())
	if !strings.Contains(b.String(), "fix the parser") {
		t.Errorf("meta lost the title:\n%s", b.String())
	}

	b.Reset()
	rows := []session.Summary{{
		Ref: session.Ref{Provider: "codex", SessionID: "a"}, Rank: 1, UpdatedAt: time.Now(),
		Title: "fix \x1b[1mthe parser\x1b[0m", Cwd: "/src/catchup", Preview: "why\x07 does it fail",
	}, {
		Ref: session.Ref{Provider: "claude", SessionID: "b"}, Rank: 2, UpdatedAt: time.Now(),
		Title: "other", Match: &session.Match{Role: session.RoleUser, Kind: session.KindMessage,
			Text: "the \x1b]0;pwned\x07egress rule\nwas wrong"},
	}}
	if err := List(&b, "", rows, session.FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	assertNoControls(t, "list", b.String())
	for _, want := range []string{"fix the parser", "the egress rule was wrong"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("list lost visible text %q:\n%s", want, b.String())
		}
	}

	// The caller's Thread is the provider's; rendering must not rewrite it.
	if th.Entries[0].Text != escapedThread().Entries[0].Text {
		t.Errorf("rendering mutated the caller's thread: %q", th.Entries[0].Text)
	}
}

// TestJSONKeepsItsOwnEscaping fixes the exception: JSON is parsed, not
// displayed, and encoding/json escapes the introducers itself. Nothing here may
// be stripped twice or stripped at all.
func TestJSONKeepsItsOwnEscaping(t *testing.T) {
	var b bytes.Buffer
	if err := Thread(&b, escapedThread(), session.FormatJSON); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.ContainsRune(out, escByte) {
		t.Errorf("json emitted a raw ESC:\n%q", out)
	}
	for _, want := range []string{
		`"session_id": "019f05d8\u001b[0m"`,
		`"why does \u001b[1mthe parser`,
		`"tool": "Ba\u001b[31msh"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("json lost %s:\n%s", want, out)
		}
	}
}

func assertNoControls(t *testing.T, what, out string) {
	t.Helper()
	for i, r := range out {
		if r == escByte || r == delRune || (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("%s: control %U survived at byte %d:\n%q", what, r, i, out)
			return
		}
	}
}
