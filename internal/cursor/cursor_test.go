package cursor

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

// buildStore writes a store.db shaped like a real cursor-agent chat: hex JSON
// metadata under meta key "0", content-addressed message blobs, and a root
// blob whose protobuf field 1 lists the message blob ids in order.
func buildStore(t *testing.T, dir, name string, messages []string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	buildStoreOn(t, db, name, messages)
}

// buildStoreOn fills an already-open database, letting the WAL test keep its
// writer connection open while the provider reads.
func buildStoreOn(t *testing.T, db *sql.DB, name string, messages []string) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`,
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}

	var root []byte
	for i, msg := range messages {
		// A fixed synthetic 32-byte id; content addressing is irrelevant to
		// the reader.
		id := make([]byte, 32)
		id[0] = byte(i + 1)
		if _, err := db.Exec(`INSERT INTO blobs (id, data) VALUES (?, ?)`, hex.EncodeToString(id), []byte(msg)); err != nil {
			t.Fatal(err)
		}
		root = append(root, 0x0a, 32) // field 1, wire type 2, length 32
		root = append(root, id...)
	}
	// Trailing non-message fields, as in real root blobs: a varint field and
	// a length-delimited bookkeeping payload the parser must skip.
	root = append(root, 0x10, 0x2a)                // field 2 varint
	root = append(root, 0x1a, 0x03, 'c', 'l', 'i') // field 3 bytes
	rootID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := db.Exec(`INSERT INTO blobs (id, data) VALUES (?, ?)`, rootID, root); err != nil {
		t.Fatal(err)
	}

	storeMeta, err := json.Marshal(map[string]any{
		"agentId":          "unused",
		"latestRootBlobId": rootID,
		"name":             name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('0', ?)`, hex.EncodeToString(storeMeta)); err != nil {
		t.Fatal(err)
	}
}

func writeChat(t *testing.T, root, ws, id, name, cwd string, updatedMs int64, messages []string) {
	t.Helper()
	dir := filepath.Join(root, "chats", ws, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"schemaVersion":1,"createdAtMs":` + jsonInt(updatedMs-10000) +
		`,"updatedAtMs":` + jsonInt(updatedMs) + `,"hasConversation":true,"cwd":` + jsonString(cwd) + `}`)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	buildStore(t, dir, name, messages)
}

func jsonInt(v int64) string { b, _ := json.Marshal(v); return string(b) }
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

var chatMessages = []string{
	`{"role":"system","content":"You are an AI coding assistant, powered by Composer."}`,
	`{"role":"user","content":"<user_info>\nOS Version: linux\n</user_info>"}`,
	`{"role":"user","content":[{"type":"text","text":"<timestamp>Friday, Jul 17, 2026</timestamp>\n<user_query>\nsupport cursor\n</user_query>"}]}`,
	`{"role":"assistant","content":[{"type":"reasoning","text":"hidden"},{"type":"text","text":"I will inspect the format."},{"type":"tool-call"}]}`,
	`{"role":"tool","content":[{"type":"tool-result","text":"tool output"}]}`,
	`{"role":"user","content":[{"type":"text","text":"<user_query>finish it</user_query>"}]}`,
	`{"role":"assistant","content":[{"type":"text","text":"done"}]}`,
}

func TestReadCursorSession(t *testing.T) {
	root := t.TempDir()
	writeChat(t, root, "ws1", "bb50fb79-2bad-49bf-84af-6de2ec378935", "Cursor support", "/home/u/src/catchup",
		time.Date(2026, 7, 17, 15, 22, 56, 0, time.UTC).UnixMilli(), chatMessages)

	p := New()
	src, err := p.Resolve(context.Background(), session.Roots{Cursor: root}, "bb50fb79-2bad-49bf-84af-6de2ec378935")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.Provider != session.ProviderCursor || src.Ref.SessionID != "bb50fb79-2bad-49bf-84af-6de2ec378935" {
		t.Fatalf("source ref = %+v", src.Ref)
	}
	if src.Metadata["cwd"] != "/home/u/src/catchup" {
		t.Fatalf("metadata = %+v", src.Metadata)
	}

	thread, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if thread.Source.Metadata["title"] != "Cursor support" {
		t.Fatalf("title = %+v", thread.Source.Metadata)
	}
	want := []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "support cursor"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "I will inspect the format."},
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "finish it"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "done"},
	}
	if len(thread.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(thread.Entries), len(want), thread.Entries)
	}
	for i, w := range want {
		g := thread.Entries[i]
		if g.Kind != w.Kind || g.Role != w.Role || g.Text != w.Text {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}
	}
	for _, e := range thread.Entries {
		if strings.Contains(e.Text, "tool output") || strings.Contains(e.Text, "hidden") ||
			strings.Contains(e.Text, "user_info") || strings.Contains(e.Text, "Composer") {
			t.Fatalf("noise leaked into timeline: %+v", thread.Entries)
		}
	}
}

func TestDefaultTitleIsIgnored(t *testing.T) {
	root := t.TempDir()
	writeChat(t, root, "ws1", "chat-a", "New Agent", "/w", time.Now().UnixMilli(), chatMessages)

	src, err := New().Resolve(context.Background(), session.Roots{Cursor: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := New().Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if got := thread.Source.Metadata["title"]; got != "" {
		t.Fatalf("placeholder title surfaced: %q", got)
	}
	// The listing hook falls back to the first user query.
	if thread.Preview() != "support cursor" {
		t.Fatalf("preview = %q", thread.Preview())
	}
}

// TestRootBlobIDsHighFieldTags proves that a field numbered 16 or above —
// whose protobuf tag takes two bytes — is skipped rather than misread, and
// that message ids after it still parse. A single-byte tag reader silently
// truncates the conversation here.
func TestRootBlobIDsHighFieldTags(t *testing.T) {
	id1 := make([]byte, 32)
	id1[0] = 1
	id2 := make([]byte, 32)
	id2[0] = 2

	var root []byte
	root = append(root, 0x0a, 32)
	root = append(root, id1...)
	// Field 16, wire type 2: tag = 16<<3|2 = 130, varint-encoded 0x82 0x01.
	root = append(root, 0x82, 0x01, 0x03, 'x', 'y', 'z')
	// Field 17, wire type 0: tag = 17<<3 = 136, varint-encoded 0x88 0x01.
	root = append(root, 0x88, 0x01, 0x2a)
	root = append(root, 0x0a, 32)
	root = append(root, id2...)

	got := rootBlobIDs(root)
	want := []string{hex.EncodeToString(id1), hex.EncodeToString(id2)}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

// TestReadsLiveWALDatabase reads a chat whose newest rows still sit in the
// -wal file of an open writer, as they do while cursor-agent is running. An
// immutable=1 open never consults the WAL and would miss the conversation.
func TestReadsLiveWALDatabase(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "chats", "ws1", "chat-live")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := []byte(`{"schemaVersion":1,"createdAtMs":1,"updatedAtMs":2,"hasConversation":true,"cwd":"/w"}`)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}

	writer, err := sql.Open("sqlite", filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA wal_autocheckpoint=0`,
	} {
		if _, err := writer.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	buildStoreOn(t, writer, "Live chat", chatMessages)
	if info, err := os.Stat(filepath.Join(dir, "store.db-wal")); err != nil || info.Size() == 0 {
		t.Fatalf("fixture rows are not in the WAL (err=%v); the test would prove nothing", err)
	}

	src, err := New().Resolve(context.Background(), session.Roots{Cursor: root}, "chat-live")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := New().Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Entries) != 4 || thread.Entries[3].Text != "done" {
		t.Fatalf("live WAL rows missing from timeline: %+v", thread.Entries)
	}
}

func TestListCursorSessions(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeChat(t, root, "ws1", "chat-new", "Cursor support", "/home/u/src/catchup", now.UnixMilli(), chatMessages)
	writeChat(t, root, "ws2", "chat-old", "Other", "/home/u/other", now.Add(-time.Hour).UnixMilli(), chatMessages)

	p := New()
	sums, err := p.List(context.Background(), session.Roots{Cursor: root}, session.ListOptions{
		Cwd:   "/home/u/src/catchup",
		Query: "finish",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "chat-new" {
		t.Fatalf("summaries = %+v", sums)
	}
	if sums[0].Rank != 1 || sums[0].Preview != "support cursor" {
		t.Fatalf("summary fields = %+v", sums[0])
	}
}

// Sanitized Cursor 3.24.12 state.vscdb shapes, inspected 2026-10-12.
// The UI reads the header order; JSONL dropped the mid-turn follow-up. Keep
// the writer open: the provider must see committed WAL data without checkpointing.
func TestDesktopHistory(t *testing.T) {
	ctx, root := context.Background(), t.TempDir()
	path := filepath.Join(root, "User", "globalStorage", "state.vscdb")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE cursorDiskKV (key TEXT PRIMARY KEY, value BLOB)`,
		`CREATE TABLE composerHeaders (composerId TEXT PRIMARY KEY, isArchived INTEGER, isSubagent INTEGER, value TEXT)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	data := `{"_v":18,"composerId":"same-id","name":"Review","lastUpdatedAt":1791840000000,"workspaceIdentifier":{"uri":{"scheme":"file","fsPath":"/project"}},"fullConversationHeadersOnly":[{"bubbleId":"u1","type":1},{"bubbleId":"a1","type":2},{"bubbleId":"u2","type":1},{"bubbleId":"t1","type":2},{"bubbleId":"t2","type":2},{"bubbleId":"missing","type":2}]}`
	if _, err := db.Exec(`INSERT INTO composerHeaders VALUES ('same-id',0,0,?)`, data); err != nil {
		t.Fatal(err)
	}
	for key, raw := range map[string]string{
		"composerData:same-id":   data,
		"bubbleId:same-id:u1":    `{"type":1,"text":"review these changes","createdAt":"2026-10-12T12:00:00Z"}`,
		"bubbleId:same-id:a1":    `{"type":2,"text":"starting","thinking":{"text":"hidden reasoning"}}`,
		"bubbleId:same-id:u2":    `{"type":1,"text":"I mean the first three PRs"}`,
		"bubbleId:same-id:t1":    `{"type":2,"toolFormerData":{"name":"run_terminal_command_v2","status":"completed","rawArgs":"{\"command\":\"false\"}","result":"{\"output\":\"failed\",\"exitCode\":1,\"rejected\":false}"}}`,
		"bubbleId:same-id:t2":    `{"type":2,"toolFormerData":{"name":"run_terminal_command_v2","status":"completed","rawArgs":"{\"command\":\"pwd\"}","result":"{\"output\":\"no error\",\"exitCode\":0}"}}`,
		"bubbleId:same-id:stale": `{"type":1,"text":"not in the selected conversation"}`,
	} {
		if _, err := db.Exec(`INSERT INTO cursorDiskKV VALUES (?,?)`, key, raw); err != nil {
			t.Fatal(err)
		}
	}
	roots := session.Roots{Cursor: t.TempDir(), CursorDesktop: root}
	writeChat(t, roots.Cursor, "ws", "same-id", "CLI", "/project", 1000, chatMessages)
	p := New()
	src, err := p.Resolve(ctx, roots, "ide:same-id")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	visible := thread.Visible(nil)
	if len(visible.Entries) != 4 || visible.Entries[2].Text != "I mean the first three PRs" || visible.Entries[3].Kind != session.KindFailure || visible.Entries[3].Input != `{"command":"false"}` {
		t.Fatalf("visible entries = %+v", visible.Entries)
	}
	if len(thread.Entries) != 6 || visible.Entries[0].Time != time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) || !strings.Contains(strings.Join(thread.Warnings, "\n"), "missing") {
		t.Fatalf("thread = %+v", thread)
	}
	rows, err := p.List(ctx, roots, session.ListOptions{Cwd: "/project"})
	if err != nil || len(rows) != 2 || rows[0].Ref.SessionID != "ide:same-id" || rows[1].Ref.SessionID != "same-id" {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	rows, err = p.List(ctx, roots, session.ListOptions{Query: "first three PRs"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("query rows = %+v, %v", rows, err)
	}
	for _, show := range [][]string{nil, {session.KindToolCall}} {
		rows, err := p.List(ctx, roots, session.ListOptions{Query: "pwd", Show: show})
		if err != nil || len(rows) != len(show) {
			t.Errorf("tool query with show=%v: rows = %+v, %v", show, rows, err)
		}
	}
	cli, err := p.Resolve(ctx, roots, "same-id")
	if err != nil || cli.Path == src.Path {
		t.Fatalf("CLI resolve = %+v, %v", cli, err)
	}
	if _, err := db.Exec(`UPDATE composerHeaders SET isArchived=1`); err != nil {
		t.Fatal(err)
	}
	rows, err = p.List(ctx, roots, session.ListOptions{})
	if err != nil || len(rows) != 1 || rows[0].Ref.SessionID != "same-id" {
		t.Fatalf("archived rows = %+v, %v", rows, err)
	}
	if _, err := p.Resolve(ctx, roots, "ide:same-id"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE cursorDiskKV SET value='{"headerLog":{"key":"unverified"}}' WHERE key='composerData:same-id'`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(ctx, src); err == nil || !strings.Contains(err.Error(), "headerLog") {
		t.Fatalf("unsupported encoding: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE composerHeaders`); err != nil {
		t.Fatal(err)
	}
	rows, err = p.List(ctx, roots, session.ListOptions{Cwd: "/project"})
	if err != nil || len(rows) != 1 || rows[0].Ref.SessionID != "same-id" {
		t.Errorf("CLI history with unreadable desktop: rows = %+v, %v", rows, err)
	}
	for _, id := range []string{"", "same-id"} {
		src, err := p.Resolve(ctx, roots, id)
		if err != nil || src.Ref.SessionID != "same-id" || len(src.Warnings) == 0 {
			t.Errorf("CLI resolve %q with unreadable desktop = %+v, %v", id, src, err)
		}
	}
	if _, err := p.Resolve(ctx, roots, "ide:same-id"); err == nil {
		t.Error("explicit desktop selection hid its error")
	}
}
