package codex

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wilbeibi/catchup/internal/session"
	"github.com/wilbeibi/catchup/internal/sqlitedb"
)

// The append-only index records renames; the last record for an ID wins.
// Newer Codex versions keep the current display name in state_5.sqlite.
// Both are optional: rollouts remain readable if the index is unavailable.
func nativeTitles(root string) map[string]string {
	titles := map[string]string{}
	if root == "" {
		return titles
	}
	if f, err := os.Open(filepath.Join(root, "session_index.jsonl")); err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var row struct {
				ID   string `json:"id"`
				Name string `json:"thread_name"`
			}
			if json.Unmarshal(scanner.Bytes(), &row) == nil && row.ID != "" {
				titles[row.ID] = row.Name
			}
		}
	}
	path := filepath.Join(root, "state_5.sqlite")
	if _, err := os.Stat(path); err != nil {
		return titles
	}
	db, err := sqlitedb.Open(path)
	if err != nil {
		return titles
	}
	defer db.Close()
	rows, err := db.Query("SELECT id, title, COALESCE(name,'') FROM threads")
	if err != nil {
		rows, err = db.Query("SELECT id, title, '' FROM threads")
	}
	if err != nil {
		return titles
	}
	defer rows.Close()
	for rows.Next() {
		var id, title, name string
		if rows.Scan(&id, &title, &name) != nil {
			continue
		}
		if name != "" {
			titles[id] = name
		} else if titles[id] == "" && title != "" {
			titles[id] = title
		}
	}
	return titles
}

func enrich(src *session.Source, titles map[string]string) {
	if title := titles[src.Ref.SessionID]; title != "" {
		src.Metadata["title"], src.Metadata["native_title"] = title, title
	}
}
