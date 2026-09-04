//go:build windows

package cli

import "testing"

func TestParseAcceptsWindowsDrivePaths(t *testing.T) {
	tests := [][]string{
		{"claude", "--dir", `C:\Users\u\proj`},
		{"fork", "--into", "claude", "--from", `c:/Users/u/handoff.md`},
	}
	for _, args := range tests {
		if _, err := Parse(args); err != nil {
			t.Errorf("Parse(%q): %v", args, err)
		}
	}
}
