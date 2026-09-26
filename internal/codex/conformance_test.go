package codex

import (
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/providercheck"
	"github.com/wilbeibi/catchup/internal/session"
)

func TestConformance(t *testing.T) {
	root := t.TempDir()
	writeRollout(t, root, "rollout-2026-06-26T21-31-46-sess-1.jsonl", rolloutOne, time.Now())
	providercheck.Check(t, New(), session.Roots{Codex: root}, providercheck.Expect{
		Timestamps: true, Failures: true, Compaction: true,
	})
}
