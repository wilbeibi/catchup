package sqlitedb

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenReadsLiveWALAndSpecialPaths(t *testing.T) {
	// Include URI-significant characters (# and % everywhere; ? on Unix where it
	// is a legal path character) so Open proves they are escaped in file: URIs.
	name := "dir#1%20test"
	if runtime.GOOS != "windows" {
		name += "?q"
	}
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "hist.sqlite")

	writer, err := sql.Open("sqlite", sqliteURI(dbPath, "mode=rwc"))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	for _, q := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE items (val TEXT)",
		"INSERT INTO items VALUES ('before-checkpoint')",
		"PRAGMA wal_checkpoint(TRUNCATE)",
		"INSERT INTO items VALUES ('in-wal-only')",
	} {
		if _, err := writer.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("writer did not create %q: %v", dbPath, err)
	}

	reader, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	defer reader.Close()

	var count int
	if err := reader.QueryRow("SELECT COUNT(*) FROM items WHERE val = 'in-wal-only'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("uncheckpointed WAL row count = %d, want 1", count)
	}

	if _, err := Open(filepath.Join(dir, "missing.sqlite")); err == nil {
		t.Fatal("Open on missing database succeeded")
	}
}
