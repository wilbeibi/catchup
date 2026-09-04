//go:build windows

package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A leading dot hides nothing on Windows, so the attribute is the only thing
// keeping the seed directory out of Explorer. The second write matters as
// much as the first: creating files inside an already-hidden directory is
// where this kind of change tends to break.
func TestSeedDirIsHidden(t *testing.T) {
	dir := t.TempDir()
	for i := range 2 {
		rel, err := writeSeedFile(dir, "codex-sess-1", "BODY")
		if err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		if _, err := os.ReadFile(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("write %d left an unreadable seed: %v", i, err)
		}
	}

	p, err := syscall.UTF16PtrFromString(filepath.Join(dir, seedDirName))
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := syscall.GetFileAttributes(p)
	if err != nil {
		t.Fatal(err)
	}
	if attrs&syscall.FILE_ATTRIBUTE_HIDDEN == 0 {
		t.Errorf("seed directory attributes = %#x, want the hidden bit set", attrs)
	}
}
