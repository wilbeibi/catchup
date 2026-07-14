package cli

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wilbeibi/catchup/internal/session"
)

func TestClampText(t *testing.T) {
	if _, ok := clampText(strings.Repeat("a", clampMaxBytes)); ok {
		t.Fatal("text at the threshold must not clamp")
	}

	text := "HEAD first line\n" + strings.Repeat("middle filler line\n", 400) + "TAIL last line"
	got, ok := clampText(text)
	if !ok {
		t.Fatalf("%d-byte text should clamp", len(text))
	}
	if !strings.HasPrefix(got, "HEAD first line\n") {
		t.Errorf("clamp lost the head:\n%.80s", got)
	}
	if !strings.HasSuffix(got, "TAIL last line") {
		t.Errorf("clamp lost the tail:\n...%s", got[len(got)-80:])
	}
	if !strings.Contains(got, "elided; rerun with --full for the full text") {
		t.Errorf("clamp missing the recovery marker:\n%s", got)
	}
	if len(got) >= len(text) {
		t.Errorf("clamp did not shrink the text: %d -> %d bytes", len(text), len(got))
	}

	// A single giant line (a blob with no newlines) still clamps, and the
	// cuts never split a multi-byte rune.
	blob := strings.Repeat("é", 4000) // 8000 bytes, zero newlines
	got, ok = clampText(blob)
	if !ok {
		t.Fatal("newline-free blob should clamp")
	}
	if !utf8.ValidString(got) {
		t.Error("clamp split a UTF-8 rune")
	}
}

func TestClampEntries(t *testing.T) {
	big := strings.Repeat("x\n", 3000) // 6000 bytes
	thread := session.Thread{Entries: []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: big},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "small reply"},
	}}

	got := clampEntries(thread)
	if got.Entries[0].Text == big || !strings.Contains(got.Entries[0].Text, "elided") {
		t.Error("oversized entry was not clamped")
	}
	if got.Entries[1].Text != "small reply" {
		t.Errorf("small entry changed: %q", got.Entries[1].Text)
	}
	if thread.Entries[0].Text != big {
		t.Error("clampEntries mutated the caller's thread")
	}
}
