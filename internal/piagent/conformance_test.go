package piagent

import (
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/providercheck"
	"github.com/wilbeibi/catchup/internal/session"
)

func TestConformance(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "--home-u-src-catchup--", "2026-06-28T02-50-19-365Z_019f-pi.jsonl", transcript, time.Now())
	providercheck.Check(t, New(), session.Roots{PiAgent: root}, providercheck.Expect{Timestamps: true})
}
