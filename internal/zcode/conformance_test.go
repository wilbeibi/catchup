package zcode

import (
	"testing"

	"github.com/wilbeibi/catchup/internal/providercheck"
	"github.com/wilbeibi/catchup/internal/session"
)

func TestConformance(t *testing.T) {
	providercheck.Check(t, New(), session.Roots{ZCode: makeDB(t)}, providercheck.Expect{Timestamps: true})
}
