// Package cursor implements session.Provider over Cursor CLI history: one
// directory per chat under <config>/chats/<workspace-hash>/<chatId>/ (config
// resolving $CURSOR_CONFIG_DIR, then $XDG_CONFIG_HOME/cursor, then
// ~/.cursor), holding a meta.json sidecar and the conversation in store.db,
// a SQLite blob store read via modernc.org/sqlite (already this module's one
// database dependency, shared with internal/opencode).
//
// Format, reverse-engineered from the cursor-agent bundle and live sessions
// on 2026-07-17 (no public source): store.db has blobs(id, data) and
// meta(key, value); meta key "0" is hex-encoded JSON carrying the chat name
// and latestRootBlobId. The root blob is a protobuf whose repeated field 1
// lists 32-byte blob ids in conversation order; each of those blobs is a
// JSON message {role, content} with content a string or an array of typed
// blocks (text, reasoning, tool-call, tool-result). What the user actually
// typed sits inside a <user_query> tag; user messages without one
// (<user_info> environment context, timestamp-only wrappers) are injected
// context, not the user. Messages carry no per-entry timestamps, so entries
// have zero Time and recency comes from meta.json's updatedAtMs.
//
// Ignored by default: system and tool roles, reasoning and tool blocks, and
// non-JSON (protobuf bookkeeping) blobs. No compaction marker is known for
// this store, so cursor threads never contain KindCompact entries.
package cursor

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wilbeibi/catchup/internal/session"

	_ "modernc.org/sqlite"
)

// Provider reads Cursor CLI chat directories.
type Provider struct{}

// New returns a Cursor provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	dirs, err := chatDirs(roots.Cursor)
	if err != nil {
		return session.Source{}, err
	}
	if len(dirs) == 0 {
		return session.Source{}, fmt.Errorf("cursor: no sessions found under %s", roots.Cursor)
	}
	if id == "" {
		return newSource(dirs[0]), nil
	}
	for _, d := range dirs {
		if filepath.Base(d.path) == id {
			return newSource(d), nil
		}
	}
	return session.Source{}, fmt.Errorf("cursor: no session with id %q", id)
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Path == "" {
		return session.Thread{}, errors.New("cursor: source has no path")
	}
	return readThread(src)
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	dirs, err := chatDirs(roots.Cursor)
	if err != nil {
		return nil, err
	}
	limit := opts.EffectiveLimit()
	q := strings.ToLower(opts.Query)
	out := make([]session.Summary, 0, limit)
	for _, d := range dirs {
		if len(out) >= limit {
			break
		}
		// meta.json answers the cwd filter without opening the database.
		if opts.Cwd != "" && d.meta.Cwd != opts.Cwd {
			continue
		}
		t, err := readThread(newSource(d))
		if err != nil || len(t.Entries) == 0 {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(t.VisibleText()), q) {
			continue
		}
		out = append(out, t.Summary())
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, nil
}

// --- enumeration ------------------------------------------------------------

type chatDir struct {
	path string // the chat directory itself
	meta metaJSON
	mod  time.Time
}

// metaJSON is the cheap sidecar cursor maintains beside store.db. The chat id
// is not in it; the directory name is the id.
type metaJSON struct {
	CreatedAtMs     int64  `json:"createdAtMs"`
	UpdatedAtMs     int64  `json:"updatedAtMs"`
	Cwd             string `json:"cwd"`
	HasConversation bool   `json:"hasConversation"`
}

// chatDirs returns every chat directory under <root>/chats, newest first.
// The layout is chats/<workspace-hash>/<chatId>/meta.json; chats that never
// held a conversation are skipped.
func chatDirs(root string) ([]chatDir, error) {
	base := filepath.Join(root, "chats")
	wss, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []chatDir
	for _, ws := range wss {
		if !ws.IsDir() {
			continue
		}
		ents, err := os.ReadDir(filepath.Join(base, ws.Name()))
		if err != nil {
			continue
		}
		for _, ent := range ents {
			if !ent.IsDir() {
				continue
			}
			path := filepath.Join(base, ws.Name(), ent.Name())
			d, ok := readMetaJSON(path)
			if !ok {
				continue
			}
			dirs = append(dirs, d)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
	return dirs, nil
}

func readMetaJSON(dir string) (chatDir, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return chatDir{}, false
	}
	var meta metaJSON
	if json.Unmarshal(raw, &meta) != nil || !meta.HasConversation {
		return chatDir{}, false
	}
	mod := time.Time{}
	if meta.UpdatedAtMs > 0 {
		mod = time.UnixMilli(meta.UpdatedAtMs)
	} else if meta.CreatedAtMs > 0 {
		mod = time.UnixMilli(meta.CreatedAtMs)
	}
	return chatDir{path: dir, meta: meta, mod: mod}, true
}

func newSource(d chatDir) session.Source {
	meta := map[string]string{}
	if d.meta.Cwd != "" {
		meta["cwd"] = d.meta.Cwd
	}
	return session.Source{
		Ref:       session.Ref{Provider: session.ProviderCursor, SessionID: filepath.Base(d.path)},
		Path:      d.path,
		UpdatedAt: d.mod,
		Metadata:  meta,
	}
}

// --- parsing ----------------------------------------------------------------

// storeMeta is the JSON hex-encoded under meta key "0" in store.db.
type storeMeta struct {
	Name             string `json:"name"`
	LatestRootBlobID string `json:"latestRootBlobId"`
}

type cursorMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // string or []block
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func readThread(src session.Source) (session.Thread, error) {
	db, err := openRO(filepath.Join(src.Path, "store.db"))
	if err != nil {
		return session.Thread{}, err
	}
	defer db.Close()

	meta, err := readStoreMeta(db)
	if err != nil {
		return session.Thread{}, fmt.Errorf("cursor: %s: %w", src.Path, err)
	}
	if title := strings.TrimSpace(meta.Name); title != "" && title != "New Agent" {
		src.Metadata["title"] = title
	}

	var warnings []string
	root, err := readBlob(db, meta.LatestRootBlobID)
	if err != nil {
		return session.Thread{Source: src, Warnings: []string{"conversation root blob missing"}}, nil
	}
	var entries []session.Entry
	for _, id := range rootBlobIDs(root) {
		data, err := readBlob(db, id)
		if err != nil {
			warnings = append(warnings, "skipped a missing message blob")
			continue
		}
		if len(data) == 0 || data[0] != '{' {
			continue // protobuf bookkeeping blob, not a message
		}
		var msg cursorMessage
		if json.Unmarshal(data, &msg) != nil {
			warnings = append(warnings, "skipped a malformed message blob")
			continue
		}
		if e, ok := messageEntry(msg); ok {
			entries = append(entries, e)
		}
	}
	return session.Thread{Source: src, Entries: entries, Warnings: warnings}, nil
}

// openRO opens the SQLite file for reading. Plain mode=ro comes first because
// store.db runs in WAL mode and a reader must consult the -wal file to see a
// live session's newest rows — an immutable open would silently serve the
// last checkpoint instead. immutable=1 remains as the fallback for the one
// state mode=ro cannot open (a crashed writer's orphaned -wal with no -shm,
// whose recovery needs write access); there the checkpointed prefix is the
// best available answer.
func openRO(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err == nil {
		if err = db.Ping(); err == nil {
			return db, nil
		}
		db.Close()
	}
	fallback, ferr := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if ferr != nil {
		return nil, err
	}
	if ferr = fallback.Ping(); ferr != nil {
		fallback.Close()
		return nil, err // the mode=ro error names the real obstacle
	}
	return fallback, nil
}

func readStoreMeta(db *sql.DB) (storeMeta, error) {
	var hexValue string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = '0'`).Scan(&hexValue); err != nil {
		return storeMeta{}, fmt.Errorf("no chat metadata: %w", err)
	}
	raw, err := hex.DecodeString(hexValue)
	if err != nil {
		return storeMeta{}, fmt.Errorf("malformed chat metadata: %w", err)
	}
	var meta storeMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return storeMeta{}, fmt.Errorf("malformed chat metadata: %w", err)
	}
	return meta, nil
}

func readBlob(db *sql.DB, id string) ([]byte, error) {
	var data []byte
	err := db.QueryRow(`SELECT data FROM blobs WHERE id = ?`, id).Scan(&data)
	return data, err
}

// rootBlobIDs extracts the ordered conversation blob ids from the root blob:
// every length-delimited protobuf field 1 holding exactly 32 bytes. Tags and
// lengths are decoded as full varints, so fields numbered 16 and above — which
// this unversioned format could grow at any time — are skipped correctly
// rather than misread. Other fields (token budgets, context bookkeeping) are
// skipped by wire type; a malformed tail just ends the scan, keeping whatever
// ids preceded it.
func rootBlobIDs(root []byte) []string {
	var ids []string
	i := 0
	for i < len(root) {
		tag, n := binary.Uvarint(root[i:])
		if n <= 0 {
			return ids
		}
		i += n
		field, wire := int(tag>>3), int(tag&7)
		switch wire {
		case 0: // varint
			_, n := binary.Uvarint(root[i:])
			if n <= 0 {
				return ids
			}
			i += n
		case 1: // 64-bit
			i += 8
		case 5: // 32-bit
			i += 4
		case 2: // length-delimited
			length64, n := binary.Uvarint(root[i:])
			if n <= 0 {
				return ids
			}
			i += n
			length := int(length64)
			if length < 0 || i+length > len(root) {
				return ids
			}
			if field == 1 && length == 32 {
				ids = append(ids, hex.EncodeToString(root[i:i+length]))
			}
			i += length
		default:
			return ids
		}
	}
	return ids
}

func messageEntry(msg cursorMessage) (session.Entry, bool) {
	switch msg.Role {
	case session.RoleUser:
		text := userQuery(extractText(msg.Content))
		if text == "" {
			return session.Entry{}, false // injected context, not the user
		}
		return session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: text}, true
	case session.RoleAssistant:
		text := extractText(msg.Content)
		if text == "" {
			return session.Entry{}, false
		}
		return session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: text}, true
	default: // system, tool
		return session.Entry{}, false
	}
}

// userQuery returns the inner text of the <user_query> tag, or "" when the
// message has none — which marks it as injected context (<user_info>,
// timestamps) rather than something the user typed.
func userQuery(text string) string {
	open := strings.Index(text, "<user_query>")
	if open < 0 {
		return ""
	}
	rest := text[open+len("<user_query>"):]
	if close := strings.Index(rest, "</user_query>"); close >= 0 {
		rest = rest[:close]
	}
	return strings.TrimSpace(rest)
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
