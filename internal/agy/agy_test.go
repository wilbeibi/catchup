package agy

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wilbeibi/catchup/internal/session"
)

const transcript = `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-06-09T21:02:00Z","content":"<USER_REQUEST>\nis every plugin updatable\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-06-09T14:02:00-07:00.\n</ADDITIONAL_METADATA>"}
{"step_index":1,"source":"SYSTEM","type":"EPHEMERAL_MESSAGE","status":"DONE","created_at":"2026-06-09T21:02:01Z","content":"reminders"}
{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-06-09T21:02:05Z","content":"Let me check the installed plugins."}
{"step_index":3,"source":"MODEL","type":"RUN_COMMAND","status":"DONE","created_at":"2026-06-09T21:02:16Z","content":"Output: xExtension-ArticleSummary"}
{"step_index":4,"source":"MODEL","type":"VIEW_FILE","status":"DONE","created_at":"2026-06-09T21:02:20Z","content":"File Path: sync.sh"}
{"step_index":5,"source":"SYSTEM","type":"CHECKPOINT","status":"DONE","created_at":"2026-06-09T21:05:00Z","content":"{{ CHECKPOINT 0 }} summary of the truncated context"}
{"step_index":6,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-06-09T21:06:00Z","content":"<USER_REQUEST>\nupdate them all\n</USER_REQUEST>"}
{"step_index":7,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-06-09T21:06:10Z","content":"All plugins updated."}
`

// writeConversation lays out one brain transcript and returns the root.
func writeConversation(t *testing.T, root, id, body string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(root, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func writeHistory(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "history.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testRoot(t *testing.T) session.Roots {
	t.Helper()
	root := t.TempDir()
	newer := time.Date(2026, 6, 9, 21, 6, 0, 0, time.UTC)
	older := newer.Add(-time.Hour)

	writeConversation(t, root, "conv-main", transcript, newer)
	writeConversation(t, root, "conv-other",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-06-09T20:00:00Z","content":"<USER_REQUEST>\nfix the deploy\n</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-06-09T20:00:05Z","content":"Deploy fixed."}
`, older)
	// A subagent conversation: has a transcript but no history.jsonl entry.
	writeConversation(t, root, "conv-sub",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-06-09T20:30:00Z","content":"<USER_REQUEST>\nrun the checks\n</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-06-09T20:30:05Z","content":"Checks pass."}
`, older.Add(30*time.Minute))

	// conv-other is a single-prompt conversation: its only history line was
	// written before the conversation id existed, so it has no conversationId
	// and is joined back by matchStart. The decoy line shares its display text
	// but sits in another workspace with a far timestamp.
	deployTS := time.Date(2026, 6, 9, 20, 0, 3, 0, time.UTC).UnixMilli()
	decoyTS := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC).UnixMilli()
	writeHistory(t, root, fmt.Sprintf(
		`{"display":"is every plugin updatable","timestamp":1,"workspace":"/home/u/freshrss","conversationId":"conv-main"}
{"display":"update them all","timestamp":2,"workspace":"/home/u/freshrss","conversationId":"conv-main"}
{"display":"fix the deploy","timestamp":%d,"workspace":"/home/u/elsewhere"}
{"display":"fix the deploy","timestamp":%d,"workspace":"/home/u/deploy"}
{"display":"an id-less line matching no transcript","timestamp":4,"workspace":"/home/u/old"}
`, decoyTS, deployTS))
	return session.Roots{Agy: root}
}

func TestReadAgySession(t *testing.T) {
	roots := testRoot(t)
	p := New()
	ctx := context.Background()

	src, err := p.Resolve(ctx, roots, "conv-main")
	if err != nil {
		t.Fatal(err)
	}
	if src.Metadata["cwd"] != "/home/u/freshrss" {
		t.Errorf("cwd = %q, want /home/u/freshrss", src.Metadata["cwd"])
	}
	if src.Metadata["title"] != "is every plugin updatable" {
		t.Errorf("title = %q, want the opening prompt", src.Metadata["title"])
	}

	thread, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	want := []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "is every plugin updatable"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "Let me check the installed plugins."},
		{Kind: session.KindCompact, Text: "{{ CHECKPOINT 0 }} summary of the truncated context"},
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "update them all"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "All plugins updated."},
	}
	if len(thread.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(thread.Entries), len(want), thread.Entries)
	}
	for i, w := range want {
		g := thread.Entries[i]
		if g.Kind != w.Kind || g.Role != w.Role || g.Text != w.Text {
			t.Errorf("entry %d = {%s %s %q}, want {%s %s %q}", i, g.Kind, g.Role, g.Text, w.Kind, w.Role, w.Text)
		}
	}
	if thread.Entries[0].Time.IsZero() {
		t.Error("entry 0 has no timestamp")
	}
}

func TestListAgySessions(t *testing.T) {
	roots := testRoot(t)
	p := New()
	ctx := context.Background()

	// Unfiltered: every conversation with a transcript, newest first.
	all, err := p.List(ctx, roots, session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d sessions, want 3", len(all))
	}
	if all[0].Ref.SessionID != "conv-main" || all[0].Rank != 1 {
		t.Errorf("newest = %s rank %d, want conv-main rank 1", all[0].Ref.SessionID, all[0].Rank)
	}

	// Cwd filter matches history.jsonl workspace and drops the subagent.
	sums, err := p.List(ctx, roots, session.ListOptions{Cwd: "/home/u/freshrss"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "conv-main" {
		t.Fatalf("cwd filter got %+v, want only conv-main", sums)
	}
	if sums[0].Preview != "is every plugin updatable" {
		t.Errorf("preview = %q", sums[0].Preview)
	}

	// Query matches visible text only.
	byQuery, err := p.List(ctx, roots, session.ListOptions{Query: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byQuery) != 1 || byQuery[0].Ref.SessionID != "conv-other" {
		t.Fatalf("query got %+v, want only conv-other", byQuery)
	}

	// conv-other's history line has no conversationId (single-prompt
	// conversation); matchStart joins it by first-turn text, and the closest
	// timestamp beats the decoy workspace sharing the same display.
	joined, err := p.List(ctx, roots, session.ListOptions{Cwd: "/home/u/deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if len(joined) != 1 || joined[0].Ref.SessionID != "conv-other" {
		t.Fatalf("cwd join got %+v, want only conv-other", joined)
	}
	if joined[0].Title != "fix the deploy" {
		t.Errorf("joined title = %q, want the opening prompt", joined[0].Title)
	}
	if wrong, _ := p.List(ctx, roots, session.ListOptions{Cwd: "/home/u/elsewhere"}); len(wrong) != 0 {
		t.Errorf("decoy workspace matched %+v, want none", wrong)
	}
}

// Resolving by id constructs the transcript path directly, so a conversation
// absent from history.jsonl (a subagent's, or one predating conversationId) is
// still reachable — just without cwd/title metadata.
func TestResolveSubagentByID(t *testing.T) {
	roots := testRoot(t)
	p := New()
	ctx := context.Background()

	src, err := p.Resolve(ctx, roots, "conv-sub")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "conv-sub" {
		t.Errorf("id = %s, want conv-sub", src.Ref.SessionID)
	}
	if src.Metadata["cwd"] != "" {
		t.Errorf("cwd = %q, want empty (conv-sub is not in history.jsonl)", src.Metadata["cwd"])
	}
	thread, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Entries) != 2 {
		t.Errorf("got %d entries, want 2", len(thread.Entries))
	}
}

func TestResolveNewestAndMissing(t *testing.T) {
	roots := testRoot(t)
	p := New()
	ctx := context.Background()

	src, err := p.Resolve(ctx, roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "conv-main" {
		t.Errorf("newest = %s, want conv-main", src.Ref.SessionID)
	}
	if src.Ref.Provider != session.ProviderAgy {
		t.Errorf("provider = %q", src.Ref.Provider)
	}
	if _, err := p.Resolve(ctx, roots, "nope"); err == nil {
		t.Error("want error for unknown id")
	}
}

func writeLogFile(t *testing.T, root, id, name, body string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(root, "brain", id, ".system_generated", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func writeSummariesDB(t *testing.T, root string, rows [][]any) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "conversation_summaries.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE conversation_summaries (
	conversation_id TEXT PRIMARY KEY,
	title TEXT NOT NULL DEFAULT "",
	workspace_uris TEXT NOT NULL DEFAULT "",
	parent_conversation_id TEXT NOT NULL DEFAULT "",
	nesting_depth INTEGER NOT NULL DEFAULT 0,
	agent_name TEXT NOT NULL DEFAULT ""
);`); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO conversation_summaries VALUES (?, ?, ?, ?, ?, ?)`, r...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMergeFullTranscriptAndStepOrder(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	// Compact has step 0 (missing from full), truncated step 2, a replayed
	// duplicate of step 2, and out-of-order steps 4 then 3.
	writeLogFile(t, root, "conv-merge", "transcript.jsonl",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-22T12:00:00Z","content":"<USER_REQUEST>\nopening question\n</USER_REQUEST>"}
{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-22T12:00:02Z","content":"head <truncated 500 bytes> tail"}
{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-22T12:00:02Z","content":"head <truncated 500 bytes> tail"}
{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-22T12:00:04Z","content":"after compact"}
{"step_index":3,"source":"SYSTEM","type":"CHECKPOINT","status":"DONE","created_at":"2026-11-22T12:00:03Z","content":"compacted summary"}
`, now)
	// Full omits step 0, keeps step 1 (missing from compact) and untruncated step 2.
	writeLogFile(t, root, "conv-merge", "transcript_full.jsonl",
		`{"step_index":1,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-22T12:00:01Z","content":"<USER_REQUEST>\nfrom full only\n</USER_REQUEST>"}
{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-22T12:00:02Z","content":"complete untruncated answer"}
{"step_index":3,"source":"SYSTEM","type":"CHECKPOINT","status":"DONE","created_at":"2026-11-22T12:00:03Z","content":"compacted summary"}
{"step_index":4,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-22T12:00:04Z","content":"after compact"}
`, now)

	p := New()
	ctx := context.Background()
	src, err := p.Resolve(ctx, session.Roots{Agy: root}, "conv-merge")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(src.Path) != "transcript_full.jsonl" {
		t.Errorf("src.Path = %q, want transcript_full.jsonl", src.Path)
	}
	thread, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	want := []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "opening question"},
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "from full only"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "complete untruncated answer"},
		{Kind: session.KindCompact, Text: "compacted summary"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "after compact"},
	}
	if len(thread.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(thread.Entries), len(want), thread.Entries)
	}
	for i, w := range want {
		g := thread.Entries[i]
		if g.Kind != w.Kind || g.Role != w.Role || g.Text != w.Text {
			t.Errorf("entry %d = {%s %s %q}, want {%s %s %q}", i, g.Kind, g.Role, g.Text, w.Kind, w.Role, w.Text)
		}
	}
	// Querying a word only present in transcript.jsonl (step 0) must still match.
	hits, err := p.List(ctx, session.Roots{Agy: root}, session.ListOptions{Query: "opening question"})
	if err != nil || len(hits) != 1 || hits[0].Ref.SessionID != "conv-merge" {
		t.Fatalf("query in compact-only step got %+v, err=%v", hits, err)
	}
}

func TestReadToolCallsFailuresStopsAndModel(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	writeLogFile(t, root, "conv-diag", "transcript_full.jsonl",
		`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-22T12:00:00Z","content":"<USER_REQUEST>\nrun checks\n</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-22T12:00:01Z","model":"gemini-4-neon-high","content":"Checking now.","tool_calls":[{"name":"read_file","args":{"Path":"/tmp/ok.go"}},{"name":"run_command","args":{"CommandLine":"git push --force"}}]}
{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-11-22T12:00:02Z","content":"file contents"}
{"step_index":3,"source":"MODEL","type":"GENERIC","status":"ERROR","created_at":"2026-11-22T12:00:03Z","content":"Permission prompt timed out"}
{"step_index":4,"source":"SYSTEM","type":"ERROR_MESSAGE","status":"DONE","created_at":"2026-11-22T12:00:04Z","error":"The model API is currently overloaded.","error_code":429,"content":""}
{"step_index":5,"source":"SYSTEM","type":"ERROR_MESSAGE","status":"DONE","created_at":"2026-11-22T12:00:05Z","content":"Error: model output error"}
`, now)

	p := New()
	ctx := context.Background()
	roots := session.Roots{Agy: root}
	src, err := p.Resolve(ctx, roots, "conv-diag")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := thread.Source.Metadata["model"]; got != "gemini-4-neon-high" {
		t.Errorf("model = %q, want gemini-4-neon-high", got)
	}
	want := []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "run checks"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "Checking now."},
		{Kind: session.KindToolCall, Tool: "read_file", Input: `{"Path":"/tmp/ok.go"}`},
		{Kind: session.KindToolCall, Tool: "run_command", Input: `{"CommandLine":"git push --force"}`},
		{Kind: session.KindFailure, Role: session.RoleTool, Tool: "run_command", Input: `{"CommandLine":"git push --force"}`, Text: "Permission prompt timed out"},
		{Kind: session.KindStop, Reason: "429", Text: "The model API is currently overloaded."},
		{Kind: session.KindStop, Reason: "error", Text: "Error: model output error"},
	}
	if len(thread.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(thread.Entries), len(want), thread.Entries)
	}
	for i, w := range want {
		g := thread.Entries[i]
		if g.Kind != w.Kind || g.Role != w.Role || g.Tool != w.Tool || g.Input != w.Input || g.Reason != w.Reason || g.Text != w.Text {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}
	}

	// Tool call inputs stay hidden from -q unless --show tools is set.
	if sums, err := p.List(ctx, roots, session.ListOptions{Query: "ok.go"}); err != nil || len(sums) != 0 {
		t.Errorf("default list matched tool arg: %+v, err=%v", sums, err)
	}
	if sums, err := p.List(ctx, roots, session.ListOptions{Query: "ok.go", Show: []string{session.KindToolCall}}); err != nil || len(sums) != 1 {
		t.Errorf("list with --show tools = %+v, err=%v", sums, err)
	}
}

func TestSummariesDBMetadataSubagentsAndSlashCommands(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	msg := func(prompt string) string {
		return fmt.Sprintf(`{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-11-22T12:00:00Z","content":"<USER_REQUEST>\n%s\n</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-11-22T12:00:05Z","content":"ok"}
`, prompt)
	}
	writeConversation(t, root, "conv-headless", msg("review the architecture"), base)
	writeConversation(t, root, "conv-slash", msg("fix formatting"), base.Add(-time.Minute))
	// Subagent has the newest mtime so bare Resolve must skip it.
	writeConversation(t, root, "conv-child", msg("subagent task"), base.Add(time.Minute))

	writeSummariesDB(t, root, [][]any{
		{"conv-headless", "Matrix Avatar Review", `["file:///tmp/my%20proj","file:///tmp/extra"]`, "", 0, ""},
		{"conv-slash", "", "", "", 0, ""},
		{"conv-child", "", `["file:///tmp/my%20proj"]`, "conv-headless", 1, "housekeeping-runner"},
	})
	writeHistory(t, root, `{"display":"/model","timestamp":1,"workspace":"/home/u/vault","conversationId":"conv-slash","type":"slash_command"}
{"display":"continue","timestamp":2,"workspace":"/home/u/vault","conversationId":"conv-slash"}
`)

	p := New()
	ctx := context.Background()
	roots := session.Roots{Agy: root}

	// Bare Resolve skips the newer subagent and lands on conv-headless.
	newest, err := p.Resolve(ctx, roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if newest.Ref.SessionID != "conv-headless" {
		t.Fatalf("newest = %s, want conv-headless", newest.Ref.SessionID)
	}
	if newest.Metadata["cwd"] != "/tmp/my proj" || newest.Metadata["title"] != "Matrix Avatar Review" || newest.Metadata["native_title"] != "Matrix Avatar Review" {
		t.Errorf("headless metadata = %+v", newest.Metadata)
	}

	// Unfiltered List hides conv-child; cwd filter finds conv-headless via DB workspace_uris.
	all, err := p.List(ctx, roots, session.ListOptions{})
	if err != nil || len(all) != 2 {
		t.Fatalf("unfiltered list = %+v, err=%v; want 2 (subagent hidden)", all, err)
	}
	byCwd, err := p.List(ctx, roots, session.ListOptions{Cwd: "/tmp/my proj"})
	if err != nil || len(byCwd) != 1 || byCwd[0].Ref.SessionID != "conv-headless" {
		t.Fatalf("cwd list = %+v, err=%v", byCwd, err)
	}

	// Native title is searchable even when absent from the transcript body.
	byTitle, err := p.List(ctx, roots, session.ListOptions{Query: "Matrix Avatar"})
	if err != nil || len(byTitle) != 1 || byTitle[0].Match == nil || byTitle[0].Match.Kind != "title" {
		t.Fatalf("title query = %+v, err=%v", byTitle, err)
	}

	// Explicit Resolve(id) still reads the subagent with its hierarchy metadata.
	child, err := p.Resolve(ctx, roots, "conv-child")
	if err != nil {
		t.Fatal(err)
	}
	if child.Metadata["parent"] != "conv-headless" || child.Metadata["relationship"] != "child" || child.Metadata["agent_role"] != "housekeeping-runner" {
		t.Errorf("child metadata = %+v", child.Metadata)
	}

	// Slash command in history.jsonl does not become the session title.
	slash, err := p.Resolve(ctx, roots, "conv-slash")
	if err != nil {
		t.Fatal(err)
	}
	if slash.Metadata["title"] != "continue" || slash.Metadata["cwd"] != "/home/u/vault" {
		t.Errorf("slash metadata = %+v, want title=continue cwd=/home/u/vault", slash.Metadata)
	}
}
