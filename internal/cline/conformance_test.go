package cline

import (
	"testing"

	"github.com/wilbeibi/catchup/internal/providercheck"
	"github.com/wilbeibi/catchup/internal/session"
)

func TestConformance(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "1784300926570_lzgyo", manifestJSON, messagesJSON)
	providercheck.Check(t, New(), session.Roots{Cline: root}, providercheck.Expect{
		Timestamps: true, Compaction: true,
	})
}
