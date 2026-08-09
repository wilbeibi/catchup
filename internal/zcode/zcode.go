// Package zcode implements session.Provider over ZCode (Z.ai) history stored
// in a SQLite database at $ZCODE_HOME/db.sqlite (default ~/.zcode/cli/db/db.sqlite).
//
// ZCode is an Electron desktop agent; it keeps sessions in a SQLite database
// with a schema close to OpenCode's (session/message/part tables, epoch-ms
// times, content blocks in the part.data JSON column). This provider opens that
// database read-only via modernc.org/sqlite (a pure-Go, cgo-free driver), the
// same dependency OpenCode already pulls in, so ZCode adds no new module.
//
// Schema reference, derived from the ZCode 3.x database (table_info on a live
// install). Useful rows:
//
//	session.{id, directory, title, time_created, time_updated, time_compacting,
//	         time_archived, parent_id} for metadata and listing. directory is
//	         the cwd; title comes from a title-generation turn; time_compacting
//	         marks a compaction point; time_archived is non-NULL for sessions
//	         in the trash and is filtered out of listings; parent_id is the
//	         fork parent (read only, not expanded).
//	message.{id, session_id, time_created, data, sequence} for role and order.
//	         data is JSON: {"role":"user|assistant", "modelID", "providerID",
//	         "agent", "time":{...}}. sequence is reliable and gap-free.
//	part.{id, message_id, session_id, time_created, data} for content blocks.
//	         data is JSON: {"type":"text|reasoning|tool|compaction|...",
//	         "text":"...", "time":{...}}. part.sequence is unreliable (mostly 0
//	         on older sessions), so parts are ordered by time_created within a
//	         message.
//
// Visible on the timeline: message role + part.type=text (grouped per message)
// and part.type=compaction as a compaction marker. Ignored by default:
// reasoning, tool (it dominates database size), step-start, step-finish, file,
// timeline, token/cost plumbing, and snapshots.
package zcode

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

// Provider reads the ZCode SQLite database. It is stateless; each call opens
// the database read-only and closes it, so concurrent ZCode writes are never
// blocked.
type Provider struct{}

// New returns a ZCode provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	db, path, err := open(roots.ZCode)
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
		return session.Thread{}, errors.New("zcode: source has no session id")
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
	db, path, err := open(roots.ZCode)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return listSessions(ctx, db, path, opts.Query, opts.Cwd, opts.EffectiveLimit())
}

// --- database access --------------------------------------------------------

func open(root string) (*sql.DB, string, error) {
	path := filepath.Join(root, "db.sqlite")
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("zcode: no database at %s", path)
	}
	db, err := openPath(path)
	return db, path, err
}

// openPath opens the database for reading. Plain mode=ro comes first because
// db.sqlite runs in WAL mode and a reader must consult the -wal file to see a
// live session's newest rows — an immutable open would silently serve the last
// checkpoint instead. immutable=1 remains as the fallback for the one state
// mode=ro cannot open (a crashed writer's orphaned -wal with no -shm, whose
// recovery needs write access); there the checkpointed prefix is the best
// available answer. This mirrors the OpenCode provider's rationale.
func openPath(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err == nil {
		if err = db.Ping(); err == nil {
			return db, nil
		}
		db.Close()
	}
	fallback, ferr := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if ferr != nil {
		return nil, fmt.Errorf("zcode: open %s: %w", path, err)
	}
	if ferr = fallback.Ping(); ferr != nil {
		fallback.Close()
		return nil, fmt.Errorf("zcode: open %s: %w", path, err)
	}
	return fallback, nil
}

// sessionColumns selects the metadata catchup reads from a session row.
// time_archived is filtered in the WHERE clause, not selected.
const sessionColumns = `id, COALESCE(title,''), COALESCE(directory,''), time_created, time_updated`

func scanSession(path string, sc interface{ Scan(...any) error }) (session.Source, error) {
	var id, title, dir string
	var created, updated int64
	if err := sc.Scan(&id, &title, &dir, &created, &updated); err != nil {
		return session.Source{}, err
	}
	md := map[string]string{}
	if title != "" {
		md["title"] = title
	}
	if dir != "" {
		md["cwd"] = dir
	}
	return session.Source{
		Ref:       session.Ref{Provider: session.ProviderZCode, SessionID: id},
		Path:      path,
		UpdatedAt: msToTime(updated),
		Metadata:  md,
	}, nil
}

func latestSession(ctx context.Context, db *sql.DB, path string) (session.Source, error) {
	row := db.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM session WHERE time_archived IS NULL ORDER BY time_updated DESC LIMIT 1`)
	src, err := scanSession(path, row)
	if errors.Is(err, sql.ErrNoRows) {
		return session.Source{}, errors.New("zcode: no sessions found")
	}
	return src, err
}

func loadSession(ctx context.Context, db *sql.DB, path, id string) (session.Source, error) {
	row := db.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM session WHERE id = ? AND time_archived IS NULL`, id)
	src, err := scanSession(path, row)
	if errors.Is(err, sql.ErrNoRows) {
		return session.Source{}, fmt.Errorf("zcode: no session with id %q", id)
	}
	return src, err
}

// --- listing ----------------------------------------------------------------

func listSessions(ctx context.Context, db *sql.DB, path, query, cwd string, limit int) ([]session.Summary, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+sessionColumns+` FROM session WHERE time_archived IS NULL ORDER BY time_updated DESC`)
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
	// One pass over the session's text/compaction parts, ordered by message
	// sequence then part time, grouping each message's text parts into a single
	// entry. message.sequence is gap-free in ZCode; part.sequence is not, so
	// parts within a message are ordered by time_created (and id as a tiebreak
	// for parts sharing a timestamp).
	rows, err := db.QueryContext(ctx,
		`SELECT m.id, m.data, m.sequence, m.time_created, p.data
		   FROM part p JOIN message m ON p.message_id = m.id
		   WHERE p.session_id = ?
		   ORDER BY m.sequence, p.time_created, p.id`,
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

	// modelID/providerID hold the model that answered. A session can mix model
	// records: real turns carry a canonical provider id like
	// "builtin:zai-coding-plan", but some assistant rows are internal/bridge
	// calls tagged with a bare UUID provider and a model id variant such as
	// "glm-5.2[1m]". setModel prefers the canonical form: once a provider id
	// containing a colon is seen, internal variants no longer override it, so
	// the surfaced model stays the one the user actually chose.
	var modelID, providerID string
	setModel := func(id, prov string) {
		if id == "" {
			return
		}
		if providerID == "" || !strings.Contains(providerID, ":") {
			// Upgrade an empty/internal value to anything that looks canonical.
			if prov == "" || strings.Contains(prov, ":") {
				modelID, providerID = id, prov
			}
		}
	}
	for rows.Next() {
		var mid, mdata string
		var mseq int
		var mtime int64
		var pdata string
		if err := rows.Scan(&mid, &mdata, &mseq, &mtime, &pdata); err != nil {
			return session.Thread{}, err
		}
		if mid != curID {
			flush()
			curID = mid
			curRole = roleOf(mdata)
			curTime = msToTime(mtime)
			if id, prov, ok := modelOf(mdata); ok {
				setModel(id, prov)
			}
		}

		switch partType(pdata) {
		case "text":
			if txt := partText(pdata); txt != "" {
				curText = append(curText, txt)
			}
		case "compaction":
			flush()
			entries = append(entries, session.Entry{Kind: session.KindCompact, Text: compactSummary(pdata), Time: msToTime(mtime)})
		}
	}
	flush()
	if err := rows.Err(); err != nil {
		return session.Thread{}, err
	}

	if modelID != "" {
		src.Metadata["model"] = modelID
	}
	if providerID != "" {
		src.Metadata["model_provider"] = providerID
	}
	return session.Thread{Source: src, Entries: entries}, nil
}

// --- small decoders ---------------------------------------------------------

// roleOf reads message.data.role, keeping only user/assistant (tool results
// and other bookkeeping roles become "" and produce no entry).
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

// modelOf reads the model from a message.data row. ZCode stores it two ways:
// assistant messages carry flat top-level "modelID"/"providerID"; user
// messages nest them under "model": {"modelID","providerID"}. The first
// non-empty hit wins for the caller (last writer on the timeline).
func modelOf(messageData string) (id, provider string, ok bool) {
	var flat struct {
		ModelID     string `json:"modelID"`
		ProviderID  string `json:"providerID"`
		ModelNested struct {
			ModelID    string `json:"modelID"`
			ProviderID string `json:"providerID"`
		} `json:"model"`
	}
	if json.Unmarshal([]byte(messageData), &flat) != nil {
		return "", "", false
	}
	if flat.ModelID != "" {
		return flat.ModelID, flat.ProviderID, true
	}
	if flat.ModelNested.ModelID != "" {
		return flat.ModelNested.ModelID, flat.ModelNested.ProviderID, true
	}
	return "", "", false
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

// compactSummary pulls a human label from a compaction part if ZCode attached
// one; the marker itself is what matters, so an empty summary is fine.
func compactSummary(partData string) string {
	var p struct {
		Reason string `json:"compactReason"`
	}
	json.Unmarshal([]byte(partData), &p)
	return p.Reason
}

func msToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
