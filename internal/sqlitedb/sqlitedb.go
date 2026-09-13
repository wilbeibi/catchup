// Package sqlitedb opens agent history databases for reading. The providers
// that keep their history in SQLite share one open policy; it lives here so the
// WAL reasoning below is stated, and fixed, in one place.
package sqlitedb

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open opens path for reading. Plain mode=ro comes first because the database
// runs in WAL mode and a reader must consult the -wal file to see a live
// session's newest rows — an immutable open would silently serve the last
// checkpoint instead. immutable=1 remains as the fallback for the one state
// mode=ro cannot open (a crashed writer's orphaned -wal with no -shm, whose
// recovery needs write access); there the checkpointed prefix is the best
// available answer.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err == nil {
		if err = db.Ping(); err == nil {
			return db, nil
		}
		db.Close()
	}
	fallback, ferr := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if ferr != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if ferr = fallback.Ping(); ferr != nil {
		fallback.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return fallback, nil
}
