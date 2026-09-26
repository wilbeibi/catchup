package grok

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/providercheck"
	"github.com/wilbeibi/catchup/internal/session"
)

const (
	sessionID = "01a0be48-8eca-7633-ba5c-f2f58d58a384"
	group     = "%2Fhome%2Fu%2Fsrc%2Fproj"
	cwd       = "/home/u/src/proj"
)

// acpLog mirrors a real grok 1.0.41 updates.jsonl: the authoritative ACP
// session/update stream. Every line carries agentTimestampMs. It holds a real
// user prompt, an assistant message, a successful tool call and result, a failed
// tool call and result, a synthetic user chunk Grok injected (promptIndex set),
// a second real prompt, a turn that ended on an error, and a compaction seam.
// Tokens are embedded so the query-filter tests can prove which records are on
// the timeline.
const acpLog = `{"timestamp":1,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"<user_query>support grok</user_query>"}},"_meta":{"agentTimestampMs":1000}}}
{"timestamp":2,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"I will read the log."}},"_meta":{"agentTimestampMs":2000}}}
{"timestamp":3,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"t1","title":"read_file","rawInput":{"path":"a.txt"}},"_meta":{"agentTimestampMs":2500}}}
{"timestamp":4,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed","content":[{"type":"content","content":{"type":"text","text":"a.txt tool-only-token"}}]},"_meta":{"agentTimestampMs":3000}}}
{"timestamp":5,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call","toolCallId":"t2","title":"run_terminal","rawInput":{"command":"go test ./..."}},"_meta":{"agentTimestampMs":3500}}}
{"timestamp":6,"method":"session/update","params":{"update":{"sessionUpdate":"tool_call_update","toolCallId":"t2","status":"failed","content":[{"type":"content","content":{"type":"text","text":"FAIL\tproj"}}]},"_meta":{"agentTimestampMs":4000}}}
{"timestamp":7,"method":"session/update","params":{"update":{"sessionUpdate":"turn_completed","stop_reason":"end_turn"},"_meta":{"agentTimestampMs":4500}}}
{"timestamp":8,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"<system-reminder>reminder_token</system-reminder>"},"_meta":{"hideFromScrollback":"True"}},"_meta":{"agentTimestampMs":4600}}}
{"timestamp":9,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"finish it"}},"_meta":{"agentTimestampMs":4700}}}
{"timestamp":10,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"done"}},"_meta":{"agentTimestampMs":4800}}}
{"timestamp":11,"method":"session/update","params":{"update":{"sessionUpdate":"turn_completed","stop_reason":"error"},"_meta":{"agentTimestampMs":4900}}}
{"timestamp":12,"method":"_x.ai/session/update","params":{"update":{"sessionUpdate":"compaction_checkpoint","checkpoint_file":"compaction_checkpoints/cp1.json"},"_meta":{"agentTimestampMs":5000}}}
`

// chatLog mirrors a real chat_history.jsonl: the lossy fallback. It has no
// timestamps and no error flags, and a compaction replaces it.
const chatLog = `{"type":"system","content":"You are Grok. system-only-token"}
{"type":"user","content":[{"type":"text","text":"<user_info>injected</user_info><user_query>support grok</user_query>"}]}
{"type":"user","content":[{"type":"text","text":"<system-reminder>reminder_token</system-reminder>"}],"synthetic_reason":"system_reminder"}
{"type":"assistant","content":"I will read the log.","tool_calls":[{"id":"t1","name":"read_file","arguments":"{\"path\":\"a.txt\"}"}],"model_id":"grok-build"}
{"type":"tool_result","tool_call_id":"t1","content":"a.txt tool-only-token"}
{"type":"user","content":[{"type":"text","text":"The user asked for grok support."}],"synthetic_reason":"compaction_meta"}
{"type":"user","content":[{"type":"text","text":"finish it"}]}
{"type":"assistant","content":"done","model_id":"grok-4.6"}
`

const checkpoint = `{"checkpoint_id":"cp1","compacted_history":[{"type":"system","content":"sys"},{"type":"user","content":[{"type":"text","text":"the compaction summary"}],"synthetic_reason":"compaction_meta"}]}`

func summaryMap(id, cwd string) map[string]any {
	return map[string]any{"info": map[string]any{"id": id, "cwd": cwd}, "num_messages": 1}
}

func summaryJSON(t *testing.T, m map[string]any) string {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// writeSession lays out one session directory. An empty body omits that file.
func writeSession(t *testing.T, root, group, id string, summary map[string]any, chat, updates string, mod time.Time) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", group, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if body == "" {
			return
		}
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	if summary != nil {
		write("summary.json", summaryJSON(t, summary))
	}
	write(chatFile, chat)
	write(updatesFile, updates)
	return dir
}

func writeCheckpoint(t *testing.T, dir string) {
	t.Helper()
	cp := filepath.Join(dir, "compaction_checkpoints")
	if err := os.MkdirAll(cp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cp, "cp1.json"), []byte(checkpoint), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rootsAt(dir string) session.Roots { return session.Roots{Grok: dir} }

func ms(n int64) time.Time { return time.UnixMilli(n) }

func wantACPEntries(t *testing.T) []session.Entry {
	t.Helper()
	return []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "support grok", Time: ms(1000)},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "I will read the log.", Time: ms(2000)},
		session.Failure("run_terminal", json.RawMessage(`{"command":"go test ./..."}`), "FAIL\tproj", ms(4000)),
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "finish it", Time: ms(4700)},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "done", Time: ms(4800)},
		{Kind: session.KindStop, Reason: "error", Time: ms(4900)},
		{Kind: session.KindCompact, Text: "the compaction summary", Time: ms(5000)},
	}
}

func wantChatEntries(t *testing.T) []session.Entry {
	t.Helper()
	return []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "support grok", Retained: true},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "I will read the log.", Retained: true},
		{Kind: session.KindCompact, Text: "The user asked for grok support."},
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "finish it"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "done"},
	}
}

func assertEntries(t *testing.T, got, want []session.Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("entries = %d, want %d:\n%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d =\n%+v\nwant\n%+v", i, got[i], want[i])
		}
	}
}

func TestReadTimelineFromUpdates(t *testing.T) {
	root := t.TempDir()
	m := summaryMap(sessionID, cwd)
	m["generated_title"] = "grok support"
	m["session_summary"] = "a longer summary"
	m["current_model_id"] = "grok-build"
	m["last_active_at"] = "2026-07-18T15:20:59.770364197Z"
	dir := writeSession(t, root, group, sessionID, m, "", acpLog, time.Now())
	writeCheckpoint(t, dir)

	p := New()
	src, err := p.Resolve(context.Background(), rootsAt(root), "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.Provider != session.ProviderGrok || src.Ref.SessionID != sessionID {
		t.Errorf("ref = %+v", src.Ref)
	}
	if src.Metadata["cwd"] != cwd || src.Metadata["title"] != "grok support" || src.Metadata["model"] != "grok-build" {
		t.Errorf("metadata = %+v", src.Metadata)
	}

	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	assertEntries(t, th.Entries, wantACPEntries(t))
	if len(th.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", th.Warnings)
	}
	providercheck.Check(t, p, rootsAt(root), providercheck.Expect{Timestamps: true, Failures: true, Compaction: true})
}

// Requirement: every entry carries the wall-clock from updates.jsonl, which is
// the whole point of reading it over chat_history.jsonl.
func TestReadEntriesCarryTimestamps(t *testing.T) {
	root := t.TempDir()
	dir := writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), "", acpLog, time.Now())
	writeCheckpoint(t, dir)

	p := New()
	src, _ := p.Resolve(context.Background(), rootsAt(root), "")
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range th.Entries {
		if e.Time.IsZero() {
			t.Errorf("entry %d has no timestamp: %+v", i, e)
		}
	}
}

// Requirement: only the prompt the user actually typed is a turn; Grok's
// synthetic wake-ups (promptIndex set) are injected context.
func TestRealUserTurnsOnly(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), "", acpLog, time.Now())

	p := New()
	src, _ := p.Resolve(context.Background(), rootsAt(root), "")
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	var users []string
	for _, e := range th.Entries {
		if e.Kind == session.KindMessage && e.Role == session.RoleUser {
			users = append(users, e.Text)
		}
	}
	if len(users) != 2 || users[0] != "support grok" || users[1] != "finish it" {
		t.Fatalf("user turns = %q, want the two real prompts only", users)
	}
	for _, e := range th.Entries {
		if strings.Contains(e.Text, "reminder_token") {
			t.Errorf("synthetic user chunk leaked onto the timeline: %+v", e)
		}
	}
}

// Requirement: the query filters the timeline, not the raw bytes. Text only in
// dropped records must not select the session.
func TestReadDropsSkippedTextFromSearch(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), chatLog, "", time.Now())

	p := New()
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"support grok", 1},      // real user turn
		{"finish", 1},            // later real user turn
		{"I will read", 1},       // assistant text
		{"system-only-token", 0}, // system prompt: dropped
		{"tool-only-token", 0},   // successful tool result: dropped
		{"reminder_token", 0},    // synthetic user: dropped
	} {
		sums, err := p.List(context.Background(), rootsAt(root), session.ListOptions{Query: tc.query})
		if err != nil {
			t.Fatal(err)
		}
		if len(sums) != tc.want {
			t.Errorf("query %q matched %d sessions, want %d", tc.query, len(sums), tc.want)
		}
	}
}

func TestReadTornFinalLine(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), "", acpLog+`{"timestamp":13,"method":"session/update","params":{"update":{"sessionUpdate":"agent_me`, time.Now())

	p := New()
	src, _ := p.Resolve(context.Background(), rootsAt(root), "")
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(th.Warnings) == 0 || !strings.Contains(th.Warnings[0], "malformed record") {
		t.Fatalf("warnings = %+v, want a malformed-record warning", th.Warnings)
	}
	// The parsed prefix survives.
	if len(th.Entries) < 5 {
		t.Fatalf("prefix lost: %+v", th.Entries)
	}
}

func TestReadUnknownSessionUpdate(t *testing.T) {
	root := t.TempDir()
	unknown := `{"timestamp":13,"method":"session/update","params":{"update":{"sessionUpdate":"future_kind"},"_meta":{"agentTimestampMs":6000}}}
`
	dir := writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), "", acpLog+unknown, time.Now())
	writeCheckpoint(t, dir)

	p := New()
	src, _ := p.Resolve(context.Background(), rootsAt(root), "")
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	assertEntries(t, th.Entries, wantACPEntries(t))
	if len(th.Warnings) != 1 || !strings.Contains(th.Warnings[0], "future_kind") {
		t.Fatalf("warnings = %+v, want future_kind named", th.Warnings)
	}
}

// Requirement: without updates.jsonl, read falls back to chat_history.jsonl and
// still produces a coherent (if lossy) timeline.
func TestReadFallsBackToChatHistory(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), chatLog, "", time.Now())

	p := New()
	src, _ := p.Resolve(context.Background(), rootsAt(root), "")
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	assertEntries(t, th.Entries, wantChatEntries(t))
	if th.Source.Metadata["model"] != "grok-4.6" {
		t.Errorf("model = %q, want grok-4.6 from the transcript", th.Source.Metadata["model"])
	}
}

// Requirement: an updates.jsonl over the size cap falls back to chat_history
// with a warning naming the omission, not an unbounded read.
func TestReadFallsBackWhenUpdatesTooLarge(t *testing.T) {
	old := updatesMaxBytes
	updatesMaxBytes = 10
	t.Cleanup(func() { updatesMaxBytes = old })

	root := t.TempDir()
	writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), chatLog, acpLog, time.Now())

	p := New()
	src, _ := p.Resolve(context.Background(), rootsAt(root), "")
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	assertEntries(t, th.Entries, wantChatEntries(t))
	if len(th.Warnings) != 1 || !strings.Contains(th.Warnings[0], "over the") {
		t.Fatalf("warnings = %+v, want an oversize warning", th.Warnings)
	}
}

// Requirement: a failed tool call becomes a KindFailure named from its tool_call
// and quoting the result, exactly once.
func TestFailureFromUpdates(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), "", acpLog, time.Now())

	p := New()
	src, _ := p.Resolve(context.Background(), rootsAt(root), "")
	th, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	var fails []session.Entry
	for _, e := range th.Entries {
		if e.Kind == session.KindFailure {
			fails = append(fails, e)
		}
	}
	if len(fails) != 1 {
		t.Fatalf("failures = %+v, want one", fails)
	}
	if fails[0].Tool != "run_terminal" || fails[0].Text != "FAIL\tproj" || fails[0].InputText() != `{"command":"go test ./..."}` {
		t.Errorf("failure = %+v", fails[0])
	}
}

func TestCompactionSeamFromUpdates(t *testing.T) {
	root := t.TempDir()
	dir := writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), "", acpLog, time.Now())
	writeCheckpoint(t, dir)

	p := New()
	src, _ := p.Resolve(context.Background(), rootsAt(root), "")
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
	if len(marks) != 1 || marks[0].Text != "the compaction summary" || !marks[0].Time.Equal(ms(5000)) {
		t.Fatalf("compaction markers = %+v", marks)
	}
}

func TestReadWithoutTranscript(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), "", "", time.Now())

	p := New()
	src, err := p.Resolve(context.Background(), rootsAt(root), "")
	if err != nil {
		t.Fatal(err)
	}
	th, err := p.Read(context.Background(), src)
	if err != nil || len(th.Entries) != 0 {
		t.Fatalf("read of a summary-only session = %+v, %v", th.Entries, err)
	}
	sums, err := p.List(context.Background(), rootsAt(root), session.ListOptions{})
	if err != nil || len(sums) != 0 {
		t.Fatalf("listing = %+v, %v", sums, err)
	}
}

// Requirement: summary.json is the index entry; a directory without one is not
// a session.
func TestDirWithoutSummaryIsInvisible(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, group, "no-summary", nil, chatLog, acpLog, time.Now())

	p := New()
	sums, err := p.List(context.Background(), rootsAt(root), session.ListOptions{})
	if err != nil || len(sums) != 0 {
		t.Fatalf("listing = %+v, %v", sums, err)
	}
	if _, err := p.Resolve(context.Background(), rootsAt(root), "no-summary"); err == nil {
		t.Fatal("resolve of a summary-less directory succeeded")
	}
}

func TestResolveNewestAndByID(t *testing.T) {
	root := t.TempDir()
	old := summaryMap("sess-old", "/home/u/src/other")
	old["last_active_at"] = "2026-07-18T15:00:00Z"
	now := summaryMap(sessionID, cwd)
	now["last_active_at"] = "2026-07-18T15:20:00Z"
	writeSession(t, root, group, sessionID, now, chatLog, acpLog, time.Now())
	writeSession(t, root, "%2Fhome%2Fu%2Fsrc%2Fother", "sess-old", old, chatLog, "", time.Now().Add(-2*time.Hour))

	p := New()
	src, err := p.Resolve(context.Background(), rootsAt(root), "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != sessionID {
		t.Fatalf("newest = %q, want %q", src.Ref.SessionID, sessionID)
	}
	src, err = p.Resolve(context.Background(), rootsAt(root), "sess-old")
	if err != nil || src.Ref.SessionID != "sess-old" {
		t.Fatalf("by id = %+v, %v", src.Ref, err)
	}
	if _, err := p.Resolve(context.Background(), rootsAt(root), "nope"); err == nil {
		t.Fatal("unknown id resolved without error")
	}
}

// Requirement: recency is summary.json's last_active_at, not the file mtime. The
// session with the newer mtime here has the older last_active_at.
func TestRecencyComesFromLastActiveNotMtime(t *testing.T) {
	root := t.TempDir()
	fresh := summaryMap("sess-fresh", cwd)
	fresh["last_active_at"] = "2026-07-18T15:20:00Z"
	stale := summaryMap("sess-stale", cwd)
	stale["last_active_at"] = "2026-07-18T14:00:00Z"
	writeSession(t, root, group, "sess-stale", stale, chatLog, "", time.Now()) // newer mtime
	writeSession(t, root, group, "sess-fresh", fresh, chatLog, "", time.Now().Add(-2*time.Hour))

	p := New()
	src, err := p.Resolve(context.Background(), rootsAt(root), "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "sess-fresh" {
		t.Fatalf("newest = %q, want sess-fresh (last_active_at wins over mtime)", src.Ref.SessionID)
	}
}

func TestListOrdersFiltersAndLimits(t *testing.T) {
	root := t.TempDir()
	here := summaryMap(sessionID, cwd)
	here["generated_title"] = "grok support"
	here["last_active_at"] = "2026-07-18T15:20:00Z"
	there := summaryMap("sess-other", "/home/u/src/other")
	there["last_active_at"] = "2026-07-18T15:00:00Z"
	writeSession(t, root, group, sessionID, here, chatLog, acpLog, time.Now())
	writeSession(t, root, "%2Fhome%2Fu%2Fsrc%2Fother", "sess-other", there, chatLog, "", time.Now().Add(-time.Hour))

	p := New()
	sums, err := p.List(context.Background(), rootsAt(root), session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 || sums[0].Ref.SessionID != sessionID || sums[0].Rank != 1 || sums[1].Rank != 2 {
		t.Fatalf("listing = %+v", sums)
	}
	if sums[0].Title != "grok support" || sums[0].Cwd != cwd || sums[0].Preview != "support grok" {
		t.Fatalf("summary[0] = %+v", sums[0])
	}

	sums, err = p.List(context.Background(), rootsAt(root), session.ListOptions{Cwd: "/home/u/src/other"})
	if err != nil || len(sums) != 1 || sums[0].Ref.SessionID != "sess-other" {
		t.Fatalf("cwd-filtered listing = %+v, %v", sums, err)
	}

	sums, err = p.List(context.Background(), rootsAt(root), session.ListOptions{Limit: 1})
	if err != nil || len(sums) != 1 {
		t.Fatalf("limit listing = %+v, %v", sums, err)
	}
}

// Requirement: the parser reads id/cwd from summary.json, never from the group
// directory name, whose spelling varies (percent-encoded, or slug+hash with a
// .cwd sibling).
func TestEncodedCwdLayouts(t *testing.T) {
	root := t.TempDir()
	a := summaryMap("sess-percent", cwd)
	b := summaryMap("sess-slug", "/home/u/src/proj")
	writeSession(t, root, "%2Fhome%2Fu%2Fsrc%2Fproj", "sess-percent", a, chatLog, "", time.Now())
	writeSession(t, root, "proj-3f2a1b", "sess-slug", b, chatLog, "", time.Now().Add(-time.Minute))

	p := New()
	for _, id := range []string{"sess-percent", "sess-slug"} {
		src, err := p.Resolve(context.Background(), rootsAt(root), id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if src.Metadata["cwd"] != cwd {
			t.Errorf("%s cwd = %q, want %q", id, src.Metadata["cwd"], cwd)
		}
	}
}

// Requirement: hidden and subagent sessions stay out of listings but --id can
// still reach them. Grok hides every session_kind beginning "subagent",
// including the "subagent_resume" spelling a plain switch would miss.
func TestListHidesSubagentsAndResolvesThemByID(t *testing.T) {
	root := t.TempDir()
	hidden := summaryMap("sess-hidden", cwd)
	hidden["hidden"] = true
	sub := summaryMap("sess-sub", cwd)
	sub["session_kind"] = "subagent"
	resume := summaryMap("sess-sub-resume", cwd)
	resume["session_kind"] = "subagent_resume"
	writeSession(t, root, group, "sess-hidden", hidden, chatLog, "", time.Now())
	writeSession(t, root, group, "sess-sub", sub, chatLog, "", time.Now())
	writeSession(t, root, group, "sess-sub-resume", resume, chatLog, "", time.Now())

	p := New()
	sums, err := p.List(context.Background(), rootsAt(root), session.ListOptions{})
	if err != nil || len(sums) != 0 {
		t.Fatalf("listing = %+v, %v, want none", sums, err)
	}
	for _, id := range []string{"sess-hidden", "sess-sub", "sess-sub-resume"} {
		if _, err := p.Resolve(context.Background(), rootsAt(root), id); err != nil {
			t.Errorf("resolve %s by id: %v", id, err)
		}
	}
}

// Requirement: an unused optimistic husk — no fork provenance, no messages, no
// title — is hidden, while a titled session with no messages is not.
func TestListHidesHusks(t *testing.T) {
	root := t.TempDir()
	husk := summaryMap("sess-husk", cwd)
	husk["num_messages"] = 0
	titled := summaryMap("sess-titled", cwd)
	titled["num_messages"] = 0
	titled["generated_title"] = "opened but not used"
	writeSession(t, root, group, "sess-husk", husk, chatLog, "", time.Now())
	writeSession(t, root, group, "sess-titled", titled, chatLog, "", time.Now())

	p := New()
	sums, err := p.List(context.Background(), rootsAt(root), session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "sess-titled" {
		t.Fatalf("listing = %+v, want only the titled session", sums)
	}
}

// Requirement: a listing reads the same authoritative source a read does, so a
// term that survives only in updates.jsonl (before a compaction) is found by a
// queried listing, and a plain listing previews the authoritative first turn.
func TestListReadsAuthoritativeSource(t *testing.T) {
	root := t.TempDir()
	updates := `{"timestamp":1,"method":"session/update","params":{"update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"from updates precompile-only-token"}},"_meta":{"agentTimestampMs":1000}}}
{"timestamp":2,"method":"session/update","params":{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"ok"}},"_meta":{"agentTimestampMs":2000}}}
`
	chat := `{"type":"user","content":[{"type":"text","text":"from chat"}]}
{"type":"assistant","content":"ok"}
`
	writeSession(t, root, group, sessionID, summaryMap(sessionID, cwd), chat, updates, time.Now())

	p := New()
	rows, err := p.List(context.Background(), rootsAt(root), session.ListOptions{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("plain listing = %+v, %v", rows, err)
	}
	if rows[0].Preview != "from updates precompile-only-token" {
		t.Errorf("preview = %q, want the authoritative first turn", rows[0].Preview)
	}

	rows, err = p.List(context.Background(), rootsAt(root), session.ListOptions{Query: "precompile-only-token"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("queried listing = %+v, %v, want the session found in updates.jsonl", rows, err)
	}
}

func TestListedIDsResolve(t *testing.T) {
	root := t.TempDir()
	m := summaryMap(sessionID, cwd)
	m["last_active_at"] = "2026-07-18T15:20:00Z"
	writeSession(t, root, group, sessionID, m, chatLog, acpLog, time.Now())

	p := New()
	rows, err := p.List(context.Background(), rootsAt(root), session.ListOptions{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	src, err := p.Resolve(context.Background(), rootsAt(root), rows[0].Ref.SessionID)
	if err != nil || src.Ref.SessionID != rows[0].Ref.SessionID {
		t.Fatalf("resolved %+v, %v", src.Ref, err)
	}
}

func TestTitleAndIDFallbacks(t *testing.T) {
	root := t.TempDir()
	bySummary := summaryMap("sess-sum", cwd)
	bySummary["session_summary"] = "from summary"
	byCwd := map[string]any{"info": map[string]any{"cwd": cwd}, "num_messages": 1}
	writeSession(t, root, group, "sess-sum", bySummary, chatLog, "", time.Now())
	writeSession(t, root, group, "sess-dirname", byCwd, chatLog, "", time.Now().Add(-time.Minute))

	p := New()
	src, err := p.Resolve(context.Background(), rootsAt(root), "sess-sum")
	if err != nil || src.Metadata["title"] != "from summary" {
		t.Fatalf("title = %q, %v", src.Metadata["title"], err)
	}
	src, err = p.Resolve(context.Background(), rootsAt(root), "sess-dirname")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "sess-dirname" {
		t.Errorf("id = %q, want the directory name", src.Ref.SessionID)
	}
	if src.Metadata["title"] != "proj" {
		t.Errorf("title = %q, want proj", src.Metadata["title"])
	}
}

func TestEmptyRoot(t *testing.T) {
	p := New()
	if _, err := p.Resolve(context.Background(), rootsAt(t.TempDir()), ""); err == nil {
		t.Fatal("resolve on empty root succeeded")
	}
	sums, err := p.List(context.Background(), rootsAt(t.TempDir()), session.ListOptions{})
	if err != nil || len(sums) != 0 {
		t.Fatalf("list on empty root = %+v, %v", sums, err)
	}
}
