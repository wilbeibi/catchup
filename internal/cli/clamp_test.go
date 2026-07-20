package cli

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wilbeibi/catchup/internal/session"
)

func TestClampText(t *testing.T) {
	if _, ok := clampText(strings.Repeat("a", clampPastedMaxBytes), clampPastedMaxBytes); ok {
		t.Fatal("text at the threshold must not clamp")
	}

	text := "HEAD first line\n" + strings.Repeat("middle filler line\n", 400) + "TAIL last line"
	got, ok := clampText(text, clampPastedMaxBytes)
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
	got, ok = clampText(blob, clampPastedMaxBytes)
	if !ok {
		t.Fatal("newline-free blob should clamp")
	}
	if !utf8.ValidString(got) {
		t.Error("clamp split a UTF-8 rune")
	}
}

func TestClampMax(t *testing.T) {
	assistant := session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant}
	user := session.Entry{Kind: session.KindMessage, Role: session.RoleUser}
	compact := session.Entry{Kind: session.KindCompact}

	if got := clampMax(user); got != clampPastedMaxBytes {
		t.Errorf("user message: got ceiling %d, want %d", got, clampPastedMaxBytes)
	}
	// Assistant turns and compaction summaries are generated prose: the high
	// ceiling, so a mid-sized analysis with tables in its middle renders whole.
	if got := clampMax(assistant); got != clampGeneratedMaxBytes {
		t.Errorf("assistant message: got ceiling %d, want %d", got, clampGeneratedMaxBytes)
	}
	if got := clampMax(compact); got != clampGeneratedMaxBytes {
		t.Errorf("compaction summary: got ceiling %d, want %d", got, clampGeneratedMaxBytes)
	}
}

func TestClampEntries(t *testing.T) {
	bigUser := strings.Repeat("x\n", 3000)     // 6000 bytes, over the pasted ceiling
	proseAsst := strings.Repeat("word ", 1400) // 7000 bytes: over 4KB, under 32KB
	blobAsst := strings.Repeat("y\n", 20000)   // 40000 bytes, over the generated ceiling
	thread := session.Thread{Entries: []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: bigUser},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: proseAsst},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: blobAsst},
	}}

	got := clampEntries(thread)
	if got.Entries[0].Text == bigUser || !strings.Contains(got.Entries[0].Text, "elided") {
		t.Error("oversized user entry was not clamped")
	}
	// The whole point of the role split: a mid-sized assistant analysis, whose
	// tables and conclusions sit in the middle, now renders whole instead of
	// losing its center to a head+tail cut.
	if got.Entries[1].Text != proseAsst {
		t.Error("mid-sized assistant entry was clamped; it should render whole")
	}
	// A pathological assistant blob past the high ceiling still clamps.
	if got.Entries[2].Text == blobAsst || !strings.Contains(got.Entries[2].Text, "elided") {
		t.Error("oversized assistant blob was not clamped")
	}
	if thread.Entries[0].Text != bigUser {
		t.Error("clampEntries mutated the caller's thread")
	}
}
