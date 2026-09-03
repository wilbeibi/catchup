package copilot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

// events mirrors a real Copilot CLI log (@github/copilot 1.0.80), with the
// event shapes taken from the schemas/session-events.schema.json that ships
// with the CLI. It carries one of each kind the provider must handle: the
// lifecycle rows, the system prompt, a tool-only assistant turn, the tool
// plumbing around it, a tool call that failed, and a sub-agent's own
// messages — which reuse the ordinary message types and are marked by the
// envelope's agentId — plus a failed compaction, a successful one, and the
// human turns and answers that are the actual conversation.
const events = `{"type":"session.start","id":"e1","timestamp":"2026-08-24T15:07:45.905Z","data":{"sessionId":"816a9dd1","version":1,"producer":"copilot-agent","context":{"cwd":"/home/u/src/catchup"}}}
{"type":"session.model_change","id":"e2","timestamp":"2026-08-24T15:07:47.149Z","data":{"newModel":"auto","reasoningEffort":null}}
{"type":"session.auto_mode_resolved","id":"e3","timestamp":"2026-08-24T15:07:47.958Z","data":{"chosenModel":"claude-haiku-4.5","routingMethod":"hydra"}}
{"type":"system.message","id":"e4","timestamp":"2026-08-24T15:07:47.999Z","data":{"role":"system","content":"You are the GitHub Copilot CLI."}}
{"type":"user.message","id":"e5","timestamp":"2026-08-24T15:07:48.023Z","data":{"content":"support copilot","transformedContent":"<current_datetime>2026-08-24T08:07:48.022-07:00</current_datetime>\n\nsupport copilot\n\n<system_reminder>injected</system_reminder>","attachments":[],"delivery":"idle"}}
{"type":"assistant.turn_start","id":"e6","timestamp":"2026-08-24T15:07:48.070Z","data":{"turnId":"0"}}
{"type":"assistant.message","id":"e7","timestamp":"2026-08-24T15:07:49.558Z","data":{"messageId":"m1","model":"claude-haiku-4.5","content":"I will read the log.","toolRequests":[],"turnId":"0"}}
{"type":"assistant.message","id":"e8","timestamp":"2026-08-24T15:07:50.100Z","data":{"messageId":"m2","model":"claude-haiku-4.5","content":"","toolRequests":[{"toolCallId":"t1","name":"bash","arguments":{"command":"ls"}}],"turnId":"0"}}
{"type":"tool.execution_start","id":"e9","timestamp":"2026-08-24T15:07:50.200Z","data":{"toolCallId":"t1","toolName":"bash","arguments":{"command":"ls"}}}
{"type":"tool.execution_complete","id":"e10","timestamp":"2026-08-24T15:07:50.300Z","data":{"toolCallId":"t1","success":true,"result":{"content":"a.txt"}}}
{"type":"tool.execution_start","id":"e10a","timestamp":"2026-08-24T15:07:50.400Z","data":{"toolCallId":"t2","toolName":"bash","arguments":{"command":"go test ./..."}}}
{"type":"tool.execution_complete","id":"e10b","timestamp":"2026-08-24T15:07:50.900Z","data":{"toolCallId":"t2","success":false,"error":{"message":"Command exited with code 1\nFAIL\tproj"}}}
{"type":"subagent.started","id":"e11","timestamp":"2026-08-24T15:07:50.950Z","data":{"toolCallId":"t1","agentName":"explore"}}
{"type":"user.message","id":"e12","agentId":"agent-7","timestamp":"2026-08-24T15:07:51.000Z","data":{"content":"sub-agent instructions"}}
{"type":"assistant.message","id":"e13","agentId":"agent-7","timestamp":"2026-08-24T15:07:51.500Z","data":{"messageId":"m9","model":"gpt-5-mini","content":"sub-agent answer"}}
{"type":"assistant.turn_end","id":"e13","timestamp":"2026-08-24T15:07:52.000Z","data":{"turnId":"0"}}
{"type":"session.compaction_complete","id":"e15","timestamp":"2026-08-24T15:07:59.000Z","data":{"success":false,"error":"model unavailable","statusCode":503}}
{"type":"session.compaction_complete","id":"e16","timestamp":"2026-08-24T15:08:00.000Z","data":{"success":true,"summaryContent":"The user asked for Copilot support; the log format is settled.","preCompactionTokens":180000,"messagesRemoved":42,"compactionTokensUsed":{"inputTokens":1234,"outputTokens":56}}}
{"type":"session.shutdown","id":"e15","timestamp":"2026-08-24T15:08:10.000Z","data":{}}
{"type":"session.resume","id":"e16","timestamp":"2026-08-24T15:08:35.352Z","data":{"eventCount":15,"context":{"cwd":"/home/u/src/catchup"}}}
{"type":"user.message","id":"e17","timestamp":"2026-08-24T15:08:37.037Z","data":{"content":"finish it","transformedContent":"wrapped"}}
{"type":"assistant.message","id":"e18","timestamp":"2026-08-24T15:08:40.397Z","data":{"messageId":"m3","model":"gpt-5.4","content":"done","toolRequests":[]}}
`

const sessionID = "816a9dd1-c2ef-4b36-99a8-b503320856cc"

const workspace = `id: ` + sessionID + `
cwd: /home/u/src/catchup
git_root: /home/u/src/catchup
repository: wilbeibi/catchup
branch: main
client_name: github/cli
name: 'Reply with exactly: hello'
user_named: false
summary_count: 0
created_at: 2026-08-24T15:07:45.818Z
updated_at: 2026-08-24T15:07:45.850Z
`

func ts(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func wantEntries(t *testing.T) []session.Entry {
	return []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "support copilot", Time: ts(t, "2026-08-24T15:07:48.023Z")},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "I will read the log.", Time: ts(t, "2026-08-24T15:07:49.558Z")},
		session.Failure("bash", json.RawMessage(`{"command":"go test ./..."}`), "Command exited with code 1\nFAIL\tproj", ts(t, "2026-08-24T15:07:50.900Z")),
		{Kind: session.KindCompact, Text: "The user asked for Copilot support; the log format is settled.", Time: ts(t, "2026-08-24T15:08:00.000Z")},
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "finish it", Time: ts(t, "2026-08-24T15:08:37.037Z")},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "done", Time: ts(t, "2026-08-24T15:08:40.397Z")},
	}
}

// writeSession lays out one session directory. A session whose workspace.yaml
// is empty stands for one killed before its metadata was written.
func writeSession(t *testing.T, root, id, ws, log string, mod time.Time) string {
	t.Helper()
	dir := filepath.Join(root, "session-state", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if ws != "" {
		if err := os.WriteFile(filepath.Join(dir, workspaceFile), []byte(ws), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, eventsFile)
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return dir
}

func rootsAt(dir string) session.Roots { return session.Roots{Copilot: dir} }

func TestReadSkipsEverythingButConversation(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, sessionID, workspace, events, time.Now())

	p := New()
	src, err := p.Resolve(context.Background(), rootsAt(root), "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.Provider != session.ProviderCopilot {
		t.Errorf("provider = %q", src.Ref.Provider)
	}
	if src.Ref.SessionID != sessionID {
		t.Errorf("session id = %q", src.Ref.SessionID)
	}
	if src.Metadata["cwd"] != "/home/u/src/catchup" {
		t.Errorf("cwd = %q", src.Metadata["cwd"])
	}
	// A single-quoted scalar keeps the colon inside the title.
	if src.Metadata["title"] != "Reply with exactly: hello" {
		t.Errorf("title = %q", src.Metadata["title"])
	}
	// Last writer wins: the session was routed to another model mid-way.
	if src.Metadata["model"] != "gpt-5.4" {
		t.Errorf("model = %q", src.Metadata["model"])
	}

	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	got, want := th.Entries, wantEntries(t)
	if len(got) != len(want) {
		t.Fatalf("entries = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d =\n%+v\nwant\n%+v", i, got[i], want[i])
		}
	}
	if len(th.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", th.Warnings)
	}
}

// A session killed mid-write leaves a torn final line. The prefix stays
// readable and the reader says what it dropped.
func TestReadTornFinalLine(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "torn", workspace, events+`{"type":"user.message","data":{"cont`, time.Now())

	p := New()
	src, err := p.Resolve(context.Background(), rootsAt(root), "")
	if err != nil {
		t.Fatal(err)
	}
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Entries) != len(wantEntries(t)) {
		t.Errorf("entries = %d, want %d", len(th.Entries), len(wantEntries(t)))
	}
	if len(th.Warnings) == 0 {
		t.Error("no warning for the torn record")
	}
}

// Without workspace.yaml the directory name is the id and the timeline is
// still whole.
func TestReadWithoutWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "no-yaml", "", events, time.Now())

	p := New()
	src, err := p.Resolve(context.Background(), rootsAt(root), "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "no-yaml" {
		t.Errorf("session id = %q, want the directory name", src.Ref.SessionID)
	}
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Entries) != len(wantEntries(t)) {
		t.Errorf("entries = %d, want %d", len(th.Entries), len(wantEntries(t)))
	}
}

// Recency is the event log's mtime, not workspace.yaml's updated_at: the yaml
// is written when metadata changes and lags a session that is still appending.
func TestListOrdersByEventLogMtime(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeSession(t, root, "older", workspace, events, now.Add(-time.Hour))
	writeSession(t, root, "newer-id", strings.Replace(workspace, sessionID, "newer-id", 1), events, now)

	got, err := New().List(context.Background(), rootsAt(root), session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("rows = %d, want 2", len(got))
	}
	if got[0].Ref.SessionID != "newer-id" || got[0].Rank != 1 {
		t.Errorf("first row = %+v", got[0])
	}
}

func TestListFiltersByCwdAndQuery(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	writeSession(t, root, "here", workspace, events, now)
	elsewhere := strings.Replace(workspace, "cwd: /home/u/src/catchup", "cwd: /home/u/src/other", 1)
	writeSession(t, root, "there", elsewhere, events, now.Add(-time.Minute))

	p := New()
	rows, err := p.List(context.Background(), rootsAt(root), session.ListOptions{Cwd: "/home/u/src/other"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Cwd != "/home/u/src/other" {
		t.Fatalf("cwd filter kept %+v", rows)
	}

	rows, err = p.List(context.Background(), rootsAt(root), session.ListOptions{Query: "sub-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("query matched sub-agent text that is not on the timeline: %+v", rows)
	}
}

// Every id a listing reports must select that session on the next run: the
// rank a user retypes resolves through this round trip.
func TestListedIDsResolve(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, sessionID, workspace, events, time.Now())

	p := New()
	rows, err := p.List(context.Background(), rootsAt(root), session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	src, err := p.Resolve(context.Background(), rootsAt(root), rows[0].Ref.SessionID)
	if err != nil {
		t.Fatalf("resolving the listed id %q: %v", rows[0].Ref.SessionID, err)
	}
	if src.Ref.SessionID != rows[0].Ref.SessionID {
		t.Errorf("resolved %q, want %q", src.Ref.SessionID, rows[0].Ref.SessionID)
	}
}

// A compaction that failed removed nothing, so it is not a seam --since-compact
// may cut on; only the successful one is a marker, and it carries the summary
// that replaced the history.
func TestCompactionMarkers(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, sessionID, workspace, events, time.Now())

	p := New()
	src, err := p.Resolve(context.Background(), rootsAt(root), "")
	if err != nil {
		t.Fatal(err)
	}
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	var marks []session.Entry
	for _, e := range th.Entries {
		if e.Kind == session.KindCompact {
			marks = append(marks, e)
		}
	}
	if len(marks) != 1 {
		t.Fatalf("compaction markers = %d, want 1 (the failed one is not a seam): %+v", len(marks), marks)
	}
	if !strings.Contains(marks[0].Text, "the log format is settled") {
		t.Errorf("marker text = %q, want the summaryContent", marks[0].Text)
	}
}

func TestResolveErrors(t *testing.T) {
	root := t.TempDir()
	p := New()
	if _, err := p.Resolve(context.Background(), rootsAt(root), ""); err == nil {
		t.Error("empty root resolved without error")
	}
	writeSession(t, root, sessionID, workspace, events, time.Now())
	if _, err := p.Resolve(context.Background(), rootsAt(root), "nope"); err == nil {
		t.Error("unknown id resolved without error")
	}
}
