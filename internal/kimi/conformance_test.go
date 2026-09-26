package kimi

import (
	"testing"

	"github.com/wilbeibi/catchup/internal/providercheck"
	"github.com/wilbeibi/catchup/internal/session"
)

func TestConformance(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "wd_catchup_abc", "session_new",
		`{"createdAt":"2026-07-17T12:45:34.477Z","updatedAt":"2026-07-17T12:45:34.628Z","title":"Kimi support","workDir":"/home/u/src/catchup"}`,
		wire14)
	providercheck.Check(t, New(), session.Roots{Kimi: root}, providercheck.Expect{Timestamps: true})
}
