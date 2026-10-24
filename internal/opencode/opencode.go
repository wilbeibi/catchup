// Package opencode implements session.Provider over OpenCode history stored in a
// SQLite database at <XDG_DATA_HOME|~/.local/share>/opencode/opencode.db.
//
// It opens the database read-only via modernc.org/sqlite (a pure-Go, cgo-free
// driver) — the module's one database dependency, shared with internal/cursor.
//
// OpenCode 2.x keeps sessions in session_v2 and their transcripts in
// session_message, one row per message ordered by seq, and its startup
// migration copies every legacy session there in the background. A database
// with session_v2 is read from it alone; one without is read from the legacy
// session/message/part tables. Schema references, read at anomalyco/opencode
// 6dfc681835 (2026-10-21):
// packages/core/src/session/sql.ts (tables) and
// packages/schema/src/session-message.ts (session_message.data per type).
// Checked against a database written by opencode2 0.0.0-beta-19271 and the
// v2.0.32 layout reported in issue #26.
//
// In 2.x, user rows and the text items of assistant rows form the timeline, a
// tool item whose state.status is error becomes a failure entry, and a
// completed compaction row becomes a compaction marker carrying its summary.
// Synthetic and system rows are injected context, not user turns.
//
// Useful legacy rows: session.{id,title,directory,parent_id,agent,model,
// time_created,time_updated} for metadata and listing (session_v2 has the same
// columns); message.{id,session_id,time_created,data} for role and order;
// part.data.type=text for timeline text; part.data.type=compaction as a
// compaction marker. Times are epoch milliseconds.
//
// A legacy part.type=tool whose state.status is error becomes a failure entry
// with state.input and state.error. Successful tool parts stay off the timeline.
// Ignored by default: other tool states, reasoning, step-start, step-finish,
// token/cost plumbing, snapshots, patch payloads, and raw file payloads. Child
// session ids can be read, but catchup does not expand parent/child trees.
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

	"github.com/wilbeibi/catchup/internal/session"
	"github.com/wilbeibi/catchup/internal/sqlitedb"
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
	v2, err := isV2(ctx, db)
	if err != nil {
		return session.Source{}, err
	}

	if id == "" {
		return latestSession(ctx, db, path, v2)
	}
	return loadSession(ctx, db, path, v2, id)
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Ref.SessionID == "" {
		return session.Thread{}, errors.New("opencode: source has no session id")
	}
	// Path is the database file, not a per-session file.
	db, err := sqlitedb.Open(src.Path)
	if err != nil {
		return session.Thread{}, fmt.Errorf("opencode: %w", err)
	}
	defer db.Close()
	v2, err := isV2(ctx, db)
	if err != nil {
		return session.Thread{}, err
	}
	if v2 {
		return readThreadV2(ctx, db, src)
	}
	return readThread(ctx, db, src)
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	db, path, err := open(roots.OpenCode)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer db.Close()
	v2, err := isV2(ctx, db)
	if err != nil {
		return nil, err
	}
	return listSessions(ctx, db, path, v2, opts)
}

// --- database access --------------------------------------------------------

func open(root string) (*sql.DB, string, error) {
	path := filepath.Join(root, "opencode.db")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("opencode: no database at %s: %w", path, fs.ErrNotExist)
	}
	db, err := sqlitedb.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("opencode: %w", err)
	}
	return db, path, nil
}

// isV2 reports whether the database has OpenCode 2.x session tables.
func isV2(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_v2'`).Scan(&n)
	return n > 0, err
}

const sessionColumns = `id, COALESCE(title,''), directory, COALESCE(agent,''), COALESCE(model,''), COALESCE(parent_id,''), time_created, time_updated`

// selectSessions selects sessionColumns from the session table of either schema.
func selectSessions(v2 bool) string {
	if v2 {
		return `SELECT ` + sessionColumns + ` FROM session_v2`
	}
	return `SELECT ` + sessionColumns + ` FROM session`
}

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

func latestSession(ctx context.Context, db *sql.DB, path string, v2 bool) (session.Source, error) {
	row := db.QueryRowContext(ctx, selectSessions(v2)+` WHERE time_archived IS NULL ORDER BY time_updated DESC LIMIT 1`)
	src, err := scanSession(path, row)
	if errors.Is(err, sql.ErrNoRows) {
		return session.Source{}, errors.New("opencode: no sessions found")
	}
	return src, err
}

func loadSession(ctx context.Context, db *sql.DB, path string, v2 bool, id string) (session.Source, error) {
	row := db.QueryRowContext(ctx, selectSessions(v2)+` WHERE id = ?`, id)
	src, err := scanSession(path, row)
	if errors.Is(err, sql.ErrNoRows) {
		return session.Source{}, fmt.Errorf("opencode: no session with id %q", id)
	}
	return src, err
}

// --- listing ----------------------------------------------------------------

func listSessions(ctx context.Context, db *sql.DB, path string, v2 bool, opts session.ListOptions) ([]session.Summary, error) {
	rows, err := db.QueryContext(ctx, selectSessions(v2)+` WHERE time_archived IS NULL ORDER BY time_updated DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matchText, firstText := matchText, firstText
	if v2 {
		matchText, firstText = matchTextV2, firstTextV2
	}

	q := strings.ToLower(opts.Query)
	limit := opts.EffectiveLimit()
	out := make([]session.Summary, 0, limit)
	for rows.Next() {
		if len(out) >= limit {
			break
		}
		src, err := scanSession(path, rows)
		if err != nil {
			return nil, err
		}
		if !opts.MatchesCwd(src.Metadata["cwd"]) {
			continue
		}
		// The query decides before the preview is fetched: a session the query
		// rejects should cost one statement, not two.
		var match *session.Match
		if q != "" {
			role, text, ok, err := matchText(ctx, db, src.Ref.SessionID, q)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			match = opts.Excerpt(session.Entry{Kind: session.KindMessage, Role: role, Text: text})
		}
		out = append(out, session.Summary{
			Ref:       src.Ref,
			UpdatedAt: src.UpdatedAt,
			Title:     src.Metadata["title"],
			Cwd:       src.Metadata["cwd"],
			Parent:    src.Metadata["parent"],
			Preview:   firstText(ctx, db, src.Ref.SessionID),
			Match:     match,
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

// matchText returns the first message text in the session that contains the
// query, and the role that wrote it, so a listing can quote the passage instead
// of only reporting that one exists. The ordering is readThread's, so the quoted
// passage is the one a reader would meet first.
//
// The match itself stays in SQL, which keeps a queried listing from loading
// sessions it is about to discard. SQLite's lower() folds ASCII only, so this
// finds slightly less than the Go matcher would on a cased non-ASCII query; the
// divergence predates excerpts and is unchanged by them.
func matchText(ctx context.Context, db *sql.DB, sessionID, lowerQuery string) (role, text string, ok bool, err error) {
	var mdata string
	var txt sql.NullString
	err = db.QueryRowContext(ctx,
		`SELECT m.data, json_extract(p.data,'$.text')
		   FROM part p JOIN message m ON p.message_id = m.id
		   WHERE p.session_id = ?
		     AND json_extract(p.data,'$.type') = 'text'
		     AND instr(lower(json_extract(p.data,'$.text')), ?) > 0
		   ORDER BY m.time_created, p.time_created, p.id
		   LIMIT 1`,
		sessionID, lowerQuery,
	).Scan(&mdata, &txt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return roleOf(mdata), txt.String, true, nil
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

// v2Texts selects a 2.x session's message text in timeline order (seq, item):
// each user row's text and each text item of an assistant row's content.
const v2Texts = `
	SELECT seq, 0 AS item, type AS role, json_extract(data,'$.text') AS text
	  FROM session_message WHERE session_id = ?1 AND type = 'user'
	UNION ALL
	SELECT m.seq, c.key, m.type, json_extract(c.value,'$.text')
	  FROM session_message m, json_each(m.data,'$.content') c
	  WHERE m.session_id = ?1 AND m.type = 'assistant' AND json_extract(c.value,'$.type') = 'text'`

// matchTextV2 is matchText over the 2.x tables.
func matchTextV2(ctx context.Context, db *sql.DB, sessionID, lowerQuery string) (role, text string, ok bool, err error) {
	err = db.QueryRowContext(ctx,
		`SELECT role, text FROM (`+v2Texts+`) WHERE instr(lower(text), ?2) > 0 ORDER BY seq, item LIMIT 1`,
		sessionID, lowerQuery,
	).Scan(&role, &text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return role, text, true, nil
}

func firstTextV2(ctx context.Context, db *sql.DB, sessionID string) string {
	var s sql.NullString
	if db.QueryRowContext(ctx, `SELECT text FROM (`+v2Texts+`) ORDER BY seq, item LIMIT 1`, sessionID).Scan(&s) != nil {
		return ""
	}
	return s.String
}

// --- timeline ---------------------------------------------------------------

func readThread(ctx context.Context, db *sql.DB, src session.Source) (session.Thread, error) {
	// One pass over the session's text/compaction parts, ordered by message then
	// part time, grouping each message's text parts into a single entry.
	rows, err := db.QueryContext(ctx,
		`SELECT m.id, m.data, m.time_created, p.time_created, p.data
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
	var unknown session.UnknownTypes
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
		var mtime, ptime int64
		if err := rows.Scan(&mid, &mdata, &mtime, &ptime, &pdata); err != nil {
			return session.Thread{}, err
		}
		if mid != curID {
			flush()
			curID = mid
			curRole = roleOf(mdata)
			curTime = msToTime(mtime)
		}

		ptype := partType(pdata)
		switch ptype {
		case "text":
			if txt := partText(pdata); txt != "" {
				curText = append(curText, txt)
			}
		case "compaction":
			flush()
			entries = append(entries, session.Entry{Kind: session.KindCompact, Time: curTime})
		case "tool":
			if failure, ok := toolFailure(pdata, msToTime(ptime)); ok {
				flush()
				entries = append(entries, failure)
			}
		case "reasoning", "step-start", "step-finish", "patch", "file", "snapshot":
			// The model's own scratch work and the turn frames around it.
		default:
			unknown.Add(ptype)
		}
	}
	flush()
	if err := rows.Err(); err != nil {
		return session.Thread{}, err
	}
	return session.Thread{Source: src, Entries: entries, Warnings: unknown.AppendTo(nil)}, nil
}

func readThreadV2(ctx context.Context, db *sql.DB, src session.Source) (session.Thread, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT type, time_created, data FROM session_message WHERE session_id = ? ORDER BY seq`,
		src.Ref.SessionID,
	)
	if err != nil {
		return session.Thread{}, err
	}
	defer rows.Close()

	var entries []session.Entry
	var unknown session.UnknownTypes
	for rows.Next() {
		var typ, data string
		var ms int64
		if err := rows.Scan(&typ, &ms, &data); err != nil {
			return session.Thread{}, err
		}
		at := msToTime(ms)
		switch typ {
		case "user":
			if txt := partText(data); txt != "" {
				entries = append(entries, session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: txt, Time: at})
			}
		case "assistant":
			entries = appendAssistant(entries, &unknown, data, at)
		case "compaction":
			// A running or failed compaction has not replaced the context.
			var c struct {
				Status  string `json:"status"`
				Summary string `json:"summary"`
			}
			if json.Unmarshal([]byte(data), &c) == nil && c.Status == "completed" {
				entries = append(entries, session.Entry{Kind: session.KindCompact, Text: c.Summary, Time: at})
			}
		case "synthetic", "system", "skill", "shell", "agent-switched", "model-switched", "location-switched", "idle":
			// Injected context, harness bookkeeping, and turn markers.
		default:
			unknown.Add(typ)
		}
	}
	if err := rows.Err(); err != nil {
		return session.Thread{}, err
	}
	return session.Thread{Source: src, Entries: entries, Warnings: unknown.AppendTo(nil)}, nil
}

// appendAssistant appends a 2.x assistant row: its text items joined into one
// message, split around any failed tool call, which becomes a failure entry.
func appendAssistant(entries []session.Entry, unknown *session.UnknownTypes, data string, at time.Time) []session.Entry {
	var m struct {
		Content []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Name  string `json:"name"`
			State struct {
				Status string          `json:"status"`
				Input  json.RawMessage `json:"input"`
				Error  struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"state"`
			Time struct {
				Created int64 `json:"created"`
			} `json:"time"`
		} `json:"content"`
	}
	json.Unmarshal([]byte(data), &m)
	var text []string
	flush := func() {
		if len(text) > 0 {
			entries = append(entries, session.Entry{
				Kind: session.KindMessage, Role: session.RoleAssistant, Text: strings.Join(text, "\n\n"), Time: at,
			})
		}
		text = nil
	}
	for _, c := range m.Content {
		switch c.Type {
		case "text":
			if c.Text != "" {
				text = append(text, c.Text)
			}
		case "tool":
			if c.State.Status == "error" {
				flush()
				ts := at
				if c.Time.Created != 0 {
					ts = msToTime(c.Time.Created)
				}
				entries = append(entries, session.Failure(c.Name, c.State.Input, c.State.Error.Message, ts))
			}
		case "reasoning":
			// The model's own scratch work.
		default:
			unknown.Add(c.Type)
		}
	}
	flush()
	return entries
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

// toolFailure converts the one OpenCode tool state that belongs on the
// timeline. The status is structural; error-looking successful output is not
// classified as a failure.
func toolFailure(partData string, at time.Time) (session.Entry, bool) {
	var p struct {
		Type  string `json:"type"`
		Tool  string `json:"tool"`
		State struct {
			Status string          `json:"status"`
			Input  json.RawMessage `json:"input"`
			Error  string          `json:"error"`
		} `json:"state"`
	}
	if json.Unmarshal([]byte(partData), &p) != nil || p.Type != "tool" || p.State.Status != "error" {
		return session.Entry{}, false
	}
	return session.Failure(p.Tool, p.State.Input, p.State.Error, at), true
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
