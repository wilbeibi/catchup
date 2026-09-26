package deepseek

import (
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/providercheck"
	"github.com/wilbeibi/catchup/internal/session"
)

func TestConformance(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "--home-u-src-xurl--", "session-abc", transcript, false, time.Now())
	providercheck.Check(t, New(), session.Roots{DeepSeek: root}, providercheck.Expect{
		Timestamps: true, Compaction: true,
	})
}
