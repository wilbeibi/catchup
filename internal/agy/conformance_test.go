package agy

import (
	"testing"

	"github.com/wilbeibi/catchup/internal/providercheck"
)

func TestConformance(t *testing.T) {
	providercheck.Check(t, New(), testRoot(t), providercheck.Expect{Timestamps: true, Compaction: true})
}
