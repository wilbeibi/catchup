package copilot

import (
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/providercheck"
)

func TestConformance(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, sessionID, workspace, events, time.Now())
	providercheck.Check(t, New(), rootsAt(root), providercheck.Expect{
		Timestamps: true, Failures: true, Compaction: true,
	})
}
