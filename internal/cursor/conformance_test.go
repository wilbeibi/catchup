package cursor

import (
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/providercheck"
	"github.com/wilbeibi/catchup/internal/session"
)

func TestConformance(t *testing.T) {
	root := t.TempDir()
	writeChat(t, root, "ws1", "bb50fb79-2bad-49bf-84af-6de2ec378935", "Cursor support", "/home/u/src/catchup",
		time.Date(2026, 7, 17, 15, 22, 56, 0, time.UTC).UnixMilli(), chatMessages)
	// Cursor chat messages carry no per-message timestamp, so timestamps are not
	// asserted; the session's own updated time is what the listing shows.
	providercheck.Check(t, New(), session.Roots{Cursor: root}, providercheck.Expect{})
}
