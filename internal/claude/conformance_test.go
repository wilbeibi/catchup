package claude

import (
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/providercheck"
	"github.com/wilbeibi/catchup/internal/session"
)

func TestConformance(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "-home-u-src-catchup", "sess-a", transcript, time.Now())
	providercheck.Check(t, New(), session.Roots{Claude: root}, providercheck.Expect{
		Timestamps: true, Failures: true, Compaction: true,
	})
}
