package session

import (
	"path/filepath"
	"runtime"
	"strings"
)

// SameDir reports whether two recorded working directories name the same
// place. Every provider stores whatever spelling its agent was handed, so a
// byte compare misses matches that differ only in separators or trailing
// slashes — and on Windows, in the case of a drive letter (c:\src vs C:\src
// are one directory). Clean settles the separators; the case fold is
// Windows-only, because folding a Unix path would match two directories that
// genuinely differ.
//
// Deliberately not os.SameFile: this runs once per session in a listing walk,
// and a stat per candidate costs more than the two real spellings are worth.
func SameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
