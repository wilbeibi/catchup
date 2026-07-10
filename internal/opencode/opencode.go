// Package opencode implements session.Provider over OpenCode history stored in a
// SQLite database at <XDG_DATA_HOME|~/.local/share>/opencode/opencode.db.
//
// This is the only provider that needs an external dependency: it opens the
// database read-only via modernc.org/sqlite (a pure-Go, cgo-free driver), which
// keeps SQLite isolated to this package and the rest of the program
// dependency-free.
//
// Schema reference, source of truth for the session/message/part tables —
// packages/core/src/session/sql.ts in the OpenCode repo:
// https://github.com/sst/opencode/blob/dev/packages/core/src/session/sql.ts
//
// Useful rows: session.{id,title,directory,parent_id,agent,model,time_created,
// time_updated} for metadata and listing; message.{id,session_id,time_created,
// data} for role and order; part.data.type=text for timeline text;
// part.data.type=compaction as a compaction marker. Times are epoch
// milliseconds.
//
// Ignored by default: part.type=tool (it dominates database size), reasoning,
// step-start, step-finish, token/cost plumbing, snapshots, patch payloads, and
// raw file payloads. Child session ids can be read, but v1 does not expand
// parent/child trees.
package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wilbeibi/catchup/internal/session"
)

// Provider reads the OpenCode SQLite database. It is stateless; each call opens
// the database read-only and closes it, so concurrent OpenCode writes are never
// blocked.
type Provider struct{}

// New returns an OpenCode provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	db, path, err := open(roots.OpenCode)
	if err != nil {
		return session.Source{}, err
	}
	defer db.Close()

	if id == "" {
		return latestSession(ctx, db, path)
	}
	return loadSession(ctx, db, path, id)
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Ref.SessionID == "" {
		return session.Thread{}, errors.New("opencode: source has no session id")
	}
	// Path is the database file; reopen read-only and read the timeline.
	db, err := openPath(src.Path)
	if err != nil {
		return session.Thread{}, err
	}
	defer db.Close()
	return readThread(ctx, db, src)
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	db, path, err := open(roots.OpenCode)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return listSessions(ctx, db, path, opts.Query, opts.Cwd, opts.EffectiveLimit())
}

// --- database access --------------------------------------------------------

func open(root string) (*sql.DB, string, error) {
	path := filepath.Join(root, "opencode.db")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("opencode: no database at %s", path)
	}
	db, err := openPath(path)
	return db, path, err
}

func openPath(path string) (*sql.DB, error) {
	// Read-only, immutable: we never write, and treating the file as immutable
	// lets us open it without disturbing a live OpenCode process.
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return nil, fmt.Errorf("opencode: open %s: %w", path, err)
	}
	return db, nil
}

const sessionColumns = `id, title, directory, COALESCE(agent,''), COALESCE(model,''), COALESCE(parent_id,''), time_created, time_updated`

func scanSession(path string, sc interface{ Scan(...any) error }) (session.Source, error) {
	var id, title, dir, agent, model, parent string
	var created, updated int64
	if err := sc.Scan(&id, &title, &dir, &agent, &model, &parent, &created, &updated); err != nil {
		return session.Source{}, err
	}
	md := map[string]string{}
	if title != "" {
		md["title"] = title
	}
	if dir != "" {
		md["cwd"] = dir
	}
	if agent != "" {
		md["agent"] = agent
	}
	if m := cleanModel(model); m != "" {
		md["model"] = m
	}
	if parent != "" {
		md["parent"] = parent
	}
	return session.Source{
		Ref:       session.Ref{Provider: session.ProviderOpenCode, SessionID: id},
		Path:      path,
		UpdatedAt: msToTime(updated),
		Metadata:  md,
	}, nil
}

func latestSession(ctx context.Context, db *sql.DB, path string) (session.Source, error) {
	row := db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM session WHERE time_archived IS NULL ORDER BY time_updated DESC LIMIT 1`)
	src, err := scanSession(path, row)
	if errors.Is(err, sql.ErrNoRows) {
		return session.Source{}, errors.New("opencode: no sessions found")
	}
	return src, err
}

func loadSession(ctx context.Context, db *sql.DB, path, id string) (session.Source, error) {
	row := db.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM session WHERE id = ?`, id)
	src, err := scanSession(path, row)
	if errors.Is(err, sql.ErrNoRows) {
		return session.Source{}, fmt.Errorf("opencode: no session with id %q", id)
	}
	return src, err
}

// --- listing ----------------------------------------------------------------

func listSessions(ctx context.Context, db *sql.DB, path, query, cwd string, limit int) ([]session.Summary, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM session WHERE time_archived IS NULL ORDER BY time_updated DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	q := strings.ToLower(query)
	out := make([]session.Summary, 0, limit)
	for rows.Next() {
		if len(out) >= limit {
			break
		}
		src, err := scanSession(path, rows)
		if err != nil {
			return nil, err
		}
		if cwd != "" && src.Metadata["cwd"] != cwd {
			continue
		}
		if q != "" {
			match, err := matchesText(ctx, db, src.Ref.SessionID, q)
			if err != nil {
				return nil, err
			}
			if !match {
				continue
			}
		}
		out = append(out, session.Summary{
			Ref:       src.Ref,
			UpdatedAt: src.UpdatedAt,
			Title:     src.Metadata["title"],
			Cwd:       src.Metadata["cwd"],
			Preview:   firstText(ctx, db, src.Ref.SessionID),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, nil
}

func matchesText(ctx context.Context, db *sql.DB, sessionID, lowerQuery string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM part
		   WHERE session_id = ?
		     AND json_extract(data,'$.type') = 'text'
		     AND instr(lower(json_extract(data,'$.text')), ?) > 0)`,
		sessionID, lowerQuery,
	).Scan(&n)
	return n == 1, err
}

func firstText(ctx context.Context, db *sql.DB, sessionID string) string {
	var s sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT json_extract(data,'$.text') FROM part
		   WHERE session_id = ? AND json_extract(data,'$.type') = 'text'
		   ORDER BY time_created LIMIT 1`,
		sessionID,
	).Scan(&s)
	if err != nil {
		return ""
	}
	return s.String
}

// --- timeline ---------------------------------------------------------------

func readThread(ctx context.Context, db *sql.DB, src session.Source) (session.Thread, error) {
	// One pass over the session's text/compaction parts, ordered by message then
	// part time, grouping each message's text parts into a single entry.
	rows, err := db.QueryContext(ctx,
		`SELECT m.id, m.data, m.time_created, p.data
		   FROM part p JOIN message m ON p.message_id = m.id
		   WHERE p.session_id = ?
		   ORDER BY m.time_created, p.time_created, p.id`,
		src.Ref.SessionID,
	)
	if err != nil {
		return session.Thread{}, err
	}
	defer rows.Close()

	var entries []session.Entry
	var curID, curRole string
	var curTime time.Time
	var curText []string

	flush := func() {
		if len(curText) > 0 && curRole != "" {
			entries = append(entries, session.Entry{
				Kind: session.KindMessage,
				Role: curRole,
				Text: strings.Join(curText, "\n\n"),
				Time: curTime,
			})
		}
		curText = nil
	}

	for rows.Next() {
		var mid, mdata, pdata string
		var mtime int64
		if err := rows.Scan(&mid, &mdata, &mtime, &pdata); err != nil {
			return session.Thread{}, err
		}
		if mid != curID {
			flush()
			curID = mid
			curRole = roleOf(mdata)
			curTime = msToTime(mtime)
		}

		switch partType(pdata) {
		case "text":
			if txt := partText(pdata); txt != "" {
				curText = append(curText, txt)
			}
		case "compaction":
			flush()
			entries = append(entries, session.Entry{Kind: session.KindCompact, Time: curTime})
		}
	}
	flush()
	if err := rows.Err(); err != nil {
		return session.Thread{}, err
	}
	return session.Thread{Source: src, Entries: entries}, nil
}

// --- small decoders ---------------------------------------------------------

func roleOf(messageData string) string {
	var m struct {
		Role string `json:"role"`
	}
	json.Unmarshal([]byte(messageData), &m)
	switch m.Role {
	case session.RoleUser, session.RoleAssistant:
		return m.Role
	default:
		return ""
	}
}

func partType(partData string) string {
	var p struct {
		Type string `json:"type"`
	}
	json.Unmarshal([]byte(partData), &p)
	return p.Type
}

func partText(partData string) string {
	var p struct {
		Text string `json:"text"`
	}
	json.Unmarshal([]byte(partData), &p)
	return p.Text
}

// cleanModel reduces the model column (sometimes a JSON object) to a readable
// "provider/id" or "id" string.
func cleanModel(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		var m struct {
			ID         string `json:"id"`
			ProviderID string `json:"providerID"`
		}
		if json.Unmarshal([]byte(s), &m) == nil && m.ID != "" {
			if m.ProviderID != "" {
				return m.ProviderID + "/" + m.ID
			}
			return m.ID
		}
	}
	return s
}

func msToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
