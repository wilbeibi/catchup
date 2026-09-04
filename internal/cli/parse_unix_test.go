//go:build !windows

package cli

import (
	"strings"
	"testing"
)

func TestParseRejectsOneLetterRemoteHostOnUnix(t *testing.T) {
	_, err := Parse([]string{"claude", "--dir", "a:/src"})
	if err == nil || !strings.Contains(err.Error(), "another machine") {
		t.Fatalf("want remote-path guidance for a:/src, got %v", err)
	}
}
