// Package grok implements session.Provider over Grok Build (xAI's `grok` CLI)
// history: one directory per session under $GROK_HOME/sessions, grouped by the
// session's working directory.
//
// Layout, verified against a live grok 1.0.41 install:
//
//	$GROK_HOME/sessions/<percent-encoded-cwd>/<session-id>/
//	  summary.json             session metadata (the index entry)
//	  updates.jsonl            the ACP session/update stream: the authoritative log
//	  chat_history.jsonl       the message list handed to the model
//	  events.jsonl, compaction_checkpoints/, subagents/, ...  (ignored)
//
// $GROK_HOME overrides the base directory; it defaults to ~/.grok. The
// percent-encoded directory name is only a grouping key — every field this
// provider needs (id, cwd) is read from summary.json, so the >255-byte
// slug+hash spelling some groups use needs no decoding here.
//
// Timeline source. updates.jsonl is Grok's own source of truth for session
// restore, and the only file that carries what a handoff needs: every entry
// with a wall-clock timestamp (`_meta.agentTimestampMs`), tool outcomes, turn
// stop reasons, and compaction seams, in order, across the whole session.
// chat_history.jsonl is smaller but lossier: it is the message list handed to
// the model, so it has no timestamps, no error flags, and — because a
// compaction replaces it — no history from before the last compaction. It is
// read only for a listing's preview and as the fallback when updates.jsonl is
// absent or implausibly large.
//
// Record mapping (ACP sessionUpdate kinds):
//
//	user_message_chunk     a real user turn, unless Grok hid it from the user's
//	                       own scrollback (hideFromScrollback: monitor events,
//	                       system reminders). A <user_query>…</user_query> body —
//	                       a mid-turn interjection — is unwrapped to the words.
//	agent_message_chunk    assistant text, concatenated per message. A message
//	                       ends when a tool is called or the turn completes.
//	agent_thought_chunk    the model's reasoning: dropped.
//	tool_call              records the call's name and input for a later result.
//	tool_call_update       status "failed" becomes a KindFailure entry here,
//	                       quoting the result; "completed" is silent.
//	task_completed         a background task with a non-zero exit code becomes a
//	                       KindFailure.
//	turn_completed         a turn with stop_reason "error" becomes a KindStop.
//	compaction_checkpoint  a KindCompact seam, its text read from the checkpoint
//	                       file's own compaction summary.
//
// Everything else Grok emits (hook_execution, plan, goal_updated, retry_state,
// background_tasks, subagent_spawned, session_recap, …) is tooling and UI state,
// named here and dropped. A kind not in this list becomes one warning.
//
// Sessions the agent itself hides (summary "hidden", or a "subagent" /
// "subagent_fork" session_kind) are left out of listings; --id still resolves
// them.
package grok

import (
	"bufio"
	"bytes"
	"context"
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
)

// Provider reads Grok session directories. It is stateless; each call re-reads
// the files, so a session Grok is still appending to is never blocked.
type Provider struct{}

// New returns a Grok provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

const (
	updatesFile = "updates.jsonl"
	chatFile    = "chat_history.jsonl"
)

// updatesMaxBytes bounds the authoritative-transcript read. A very long
// session's updates.jsonl can run to hundreds of MB (every streamed chunk and
// hook run); past this the provider falls back to the smaller chat_history.jsonl
// and says so, rather than streaming an unbounded file on every read. A var so
// tests can lower it.
var updatesMaxBytes int64 = 512 << 20

// scanLine bounds one updates.jsonl line.
const scanLine = 64 << 20

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	infos, err := enumerate(roots.Grok)
	if err != nil {
		return session.Source{}, err
	}
	if id == "" {
		for _, in := range infos {
			if in.visible() {
				return in.source(), nil
			}
		}
		return session.Source{}, fmt.Errorf("grok: no sessions found under %s", roots.Grok)
	}
	for _, in := range infos {
		if in.sum.Info.ID == id || filepath.Base(in.dir) == id {
			return in.source(), nil
		}
	}
	return session.Source{}, fmt.Errorf("grok: no session with id %q", id)
}

// readThread builds a session's timeline from its authoritative source:
// updates.jsonl when present and within updatesMaxBytes, else chat_history.jsonl.
// stopAfterUser asks for the cheap preview read — stop once the first real user
// turn is seen — which a plain listing uses; a full read, and a queried listing,
// see the whole session.
func readThread(src session.Source, stopAfterUser bool) (session.Thread, error) {
	updates := filepath.Join(src.Path, updatesFile)
	if info, err := os.Stat(updates); err == nil {
		if info.Size() > updatesMaxBytes {
			t, err := readChat(src, filepath.Join(src.Path, chatFile))
			if err != nil {
				return t, err
			}
			t.Warnings = append(t.Warnings, fmt.Sprintf(
				"timestamps, tool failures, and stop reasons omitted: updates.jsonl is %d MB (over the %d MB limit); falling back to chat_history.jsonl",
				info.Size()>>20, updatesMaxBytes>>20))
			return t, nil
		}
		t, err := readUpdates(src, updates, stopAfterUser)
		if err != nil {
			return t, err
		}
		if len(t.Entries) > 0 {
			return t, nil
		}
	}
	return readChat(src, filepath.Join(src.Path, chatFile))
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Path == "" {
		return session.Thread{}, errors.New("grok: source has no path")
	}
	return readThread(src, false)
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	infos, err := enumerate(roots.Grok)
	if err != nil {
		return nil, err
	}
	limit := opts.EffectiveLimit()
	out := make([]session.Summary, 0, limit)
	for _, in := range infos {
		if len(out) >= limit {
			break
		}
		if !in.visible() {
			continue
		}
		src := in.source()
		if !opts.MatchesCwd(src.Metadata["cwd"]) {
			continue
		}
		// A query must see the whole session, exactly as a read does — a term
		// that only exists before a compaction is still in updates.jsonl. A
		// plain listing needs only a preview, so it stops at the first turn.
		t, err := readThread(src, opts.Query == "")
		if err != nil || len(t.Entries) == 0 {
			continue
		}
		if !opts.Matches(t) {
			continue
		}
		out = append(out, opts.Summarize(t))
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, nil
}

// --- session index ----------------------------------------------------------

// grokSummary mirrors the fields of summary.json this provider uses. It is
// deliberately partial: Grok's summary carries many more keys and grows, and an
// unknown key must not fail a parse.
type grokSummary struct {
	Info struct {
		ID  string `json:"id"`
		Cwd string `json:"cwd"`
	} `json:"info"`
	SessionSummary  string `json:"session_summary"`
	GeneratedTitle  string `json:"generated_title"`
	CurrentModelID  string `json:"current_model_id"`
	UpdatedAt       string `json:"updated_at"`
	LastActiveAt    string `json:"last_active_at"`
	SessionKind     string `json:"session_kind"`
	ParentSessionID string `json:"parent_session_id"`
	ForkedAt        string `json:"forked_at"`
	NumMessages     int    `json:"num_messages"`
	Hidden          *bool  `json:"hidden"`
}

// sessionInfo is one located session: its directory, parsed summary, and
// recency.
type sessionInfo struct {
	dir  string
	sum  grokSummary
	when time.Time
}

// visible reports whether the agent's own listing would show this session. It
// mirrors Grok's Summary::is_hidden and is_unused_optimistic_husk: a subagent
// (any session_kind starting "subagent") and an explicit hidden flag are out,
// and so is an unused optimistic husk — no fork provenance, no messages, and no
// title. Showing them would flood a listing.
func (in sessionInfo) visible() bool {
	if in.sum.Hidden != nil && *in.sum.Hidden {
		return false
	}
	if strings.HasPrefix(in.sum.SessionKind, "subagent") {
		return false
	}
	return !in.isHusk()
}

// isHusk mirrors Grok's is_unused_optimistic_husk: a TUI session opened and
// abandoned before it had a title or a message. Fork provenance exempts one.
func (in sessionInfo) isHusk() bool {
	if in.sum.SessionKind == "fork" || in.sum.ParentSessionID != "" || in.sum.ForkedAt != "" {
		return false
	}
	return in.sum.NumMessages == 0 && in.displayTitle() == ""
}

func (in sessionInfo) source() session.Source {
	id := in.sum.Info.ID
	if id == "" {
		id = filepath.Base(in.dir)
	}
	md := map[string]string{}
	if cwd := in.sum.Info.Cwd; cwd != "" {
		md["cwd"] = cwd
	}
	if title := in.title(); title != "" {
		md["title"] = title
	}
	if in.sum.CurrentModelID != "" {
		md["model"] = in.sum.CurrentModelID
	}
	return session.Source{
		Ref:       session.Ref{Provider: session.ProviderGrok, SessionID: id},
		Path:      in.dir,
		UpdatedAt: in.when,
		Metadata:  md,
	}
}

// title prefers the model-generated title, falls back to the session summary,
// then to the directory name — the same fallback every provider's listing uses
// for a session its agent never named.
func (in sessionInfo) title() string {
	if t := in.displayTitle(); t != "" {
		return t
	}
	if cwd := in.sum.Info.Cwd; cwd != "" {
		return filepath.Base(cwd)
	}
	return ""
}

// displayTitle is Grok's Summary::display_title: the generated title, else the
// session summary, with no directory-name fallback.
func (in sessionInfo) displayTitle() string {
	if t := strings.TrimSpace(in.sum.GeneratedTitle); t != "" {
		return t
	}
	return strings.TrimSpace(in.sum.SessionSummary)
}

// enumerate walks every summary.json under <root>/sessions and returns the
// sessions newest-first. A session directory without a readable summary.json is
// skipped: summary.json is the index entry, and Grok itself treats a directory
// without one as an images-only stub rather than a session.
func enumerate(root string) ([]sessionInfo, error) {
	dir := filepath.Join(root, "sessions")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var out []sessionInfo
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "summary.json" {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return nil
		}
		var s grokSummary
		if json.Unmarshal(b, &s) != nil {
			return nil
		}
		out = append(out, sessionInfo{dir: filepath.Dir(p), sum: s, when: summaryTime(s, p)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Newest first. last_active_at is Grok's own recency field (it advances only
	// when content is added), so it outranks updated_at, which metadata-only
	// writes also touch; the file mtime is the last resort. The id breaks ties so
	// the order is deterministic.
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].when.Equal(out[j].when) {
			return out[i].when.After(out[j].when)
		}
		return out[i].sum.Info.ID < out[j].sum.Info.ID
	})
	return out, nil
}

// summaryTime reads a session's recency, falling back from last_active_at to
// updated_at to the summary file's own mtime.
func summaryTime(s grokSummary, summaryPath string) time.Time {
	if t := session.ParseTime(s.LastActiveAt); !t.IsZero() {
		return t
	}
	if t := session.ParseTime(s.UpdatedAt); !t.IsZero() {
		return t
	}
	if info, err := os.Stat(summaryPath); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

// --- authoritative timeline (updates.jsonl) ---------------------------------

// acpLine is one updates.jsonl record: a JSON-RPC notification whose params
// carry the discriminated update (kept raw and decoded per kind below) and an
// outer _meta with the millisecond wall-clock.
type acpLine struct {
	Timestamp int64 `json:"timestamp"`
	Params    struct {
		Update json.RawMessage `json:"update"`
		Meta   struct {
			AgentTimestampMs int64 `json:"agentTimestampMs"`
		} `json:"_meta"`
	} `json:"params"`
}

// acpKind decodes just the discriminator of an update.
type acpKind struct {
	SessionUpdate string `json:"sessionUpdate"`
}

// contentObject is a single content block, the shape a message chunk carries.
type contentObject struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// The per-kind update shapes. Each is decoded independently from the raw
// update because one field name carries different shapes across kinds (content
// is an object for a message chunk and an array for a tool result), and one
// shared struct would fail a whole line on the first mismatch.
type acpUserChunk struct {
	Content contentObject `json:"content"`
	Meta    struct {
		HideFromScrollback json.RawMessage `json:"hideFromScrollback"`
	} `json:"_meta"`
}

type acpAssistantChunk struct {
	Content contentObject `json:"content"`
}

type acpToolCall struct {
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title"`
	RawInput   json.RawMessage `json:"rawInput"`
	Meta       struct {
		Tool struct {
			Name string `json:"name"`
		} `json:"x.ai/tool"`
	} `json:"_meta"`
}

type acpToolUpdate struct {
	ToolCallID string          `json:"toolCallId"`
	Status     string          `json:"status"`
	Title      string          `json:"title"`
	Content    json.RawMessage `json:"content"`
	RawOutput  json.RawMessage `json:"rawOutput"`
}

type acpTaskCompleted struct {
	TaskSnapshot struct {
		TaskID   string `json:"task_id"`
		Output   string `json:"output"`
		ExitCode *int   `json:"exit_code"`
	} `json:"task_snapshot"`
}

type acpTurnCompleted struct {
	StopReason string `json:"stop_reason"`
}

type acpCompaction struct {
	CheckpointFile string `json:"checkpoint_file"`
}

// acpIgnored names every updates.jsonl kind that is tooling or UI state rather
// than conversation, so a kind Grok adds later lands in the unknown warning
// instead of being silently dropped.
var acpIgnored = map[string]bool{
	"agent_thought_chunk": true, "hook_execution": true, "hook_annotation": true,
	"plan": true, "goal_updated": true, "retry_state": true,
	"background_tasks": true, "task_backgrounded": true,
	"subagent_spawned": true, "subagent_finished": true,
	"session_recap": true, "auto_compact_started": true, "auto_compact_completed": true,
	"current_mode_update": true, "memory_dream_queued": true, "memory_dream_started": true,
	"memory_dream_completed": true, "image_compressed": true, "model_changed": true,
	"available_commands_update": true, "pending_interaction": true, "interaction_resolved": true,
	"session_summary_generated": true, "tool_call_delta_chunk": true,
}

// readUpdates parses updates.jsonl into the visible timeline, carrying a
// timestamp on every entry. stopAfterUser returns as soon as the first real user
// turn is read, which is all a listing preview needs; a full read sees the whole
// session.
func readUpdates(src session.Source, path string, stopAfterUser bool) (session.Thread, error) {
	f, err := os.Open(path)
	if err != nil {
		return session.Thread{}, err
	}
	defer f.Close()

	var entries []session.Entry
	var warnings []string
	var unknown session.UnknownTypes
	calls := map[string]toolCall{}
	answered := map[string]bool{}
	var abuf strings.Builder
	var atime time.Time
	gotUser := false

	flush := func() {
		if abuf.Len() == 0 {
			return
		}
		entries = append(entries, session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: abuf.String(), Time: atime})
		abuf.Reset()
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), scanLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec acpLine
		if err := json.Unmarshal(line, &rec); err != nil {
			warnings = append(warnings, session.ReadStopWarning(err))
			break
		}
		var kind acpKind
		if json.Unmarshal(rec.Params.Update, &kind) != nil {
			continue
		}
		ts := lineTime(rec.Params.Meta.AgentTimestampMs, rec.Timestamp)
		raw := rec.Params.Update

		switch kind.SessionUpdate {
		case "user_message_chunk":
			var u acpUserChunk
			if json.Unmarshal(raw, &u) != nil || u.Content.Type != "text" {
				continue
			}
			// Grok marks injected context it hides from the user's own
			// scrollback (monitor events, system reminders) with
			// hideFromScrollback. Everything else is a real turn: a typed
			// prompt, or a mid-turn interjection whose text is wrapped in
			// <user_query>, which extractUserQuery unwraps.
			if hideBool(u.Meta.HideFromScrollback) {
				continue
			}
			q := extractUserQuery(u.Content.Text)
			if q == "" {
				continue
			}
			flush()
			entries = append(entries, session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: q, Time: ts})
			gotUser = true
		case "agent_message_chunk":
			var a acpAssistantChunk
			if json.Unmarshal(raw, &a) != nil || a.Content.Type != "text" || a.Content.Text == "" {
				continue
			}
			if abuf.Len() == 0 {
				atime = ts
			}
			abuf.WriteString(a.Content.Text)
		case "tool_call":
			var tc acpToolCall
			if json.Unmarshal(raw, &tc) != nil || tc.ToolCallID == "" {
				continue
			}
			flush()
			name := tc.Title
			if name == "" {
				name = tc.Meta.Tool.Name
			}
			calls[tc.ToolCallID] = toolCall{name: name, input: compactRaw(tc.RawInput)}
		case "tool_call_update":
			var tu acpToolUpdate
			if json.Unmarshal(raw, &tu) != nil || tu.Status != "failed" || tu.ToolCallID == "" || answered[tu.ToolCallID] {
				continue
			}
			answered[tu.ToolCallID] = true
			flush()
			c := calls[tu.ToolCallID]
			name := c.name
			if name == "" {
				name = tu.Title
			}
			entries = append(entries, session.Failure(name, json.RawMessage(c.input), toolFailureText(tu.Content, tu.RawOutput), ts))
		case "task_completed":
			var t acpTaskCompleted
			if json.Unmarshal(raw, &t) != nil {
				continue
			}
			snap := t.TaskSnapshot
			if snap.ExitCode == nil || *snap.ExitCode == 0 || answered[snap.TaskID] {
				continue
			}
			answered[snap.TaskID] = true
			flush()
			c := calls[snap.TaskID]
			entries = append(entries, session.Failure(c.name, json.RawMessage(c.input), snap.Output, ts))
		case "turn_completed":
			var t acpTurnCompleted
			if json.Unmarshal(raw, &t) != nil {
				continue
			}
			flush()
			if t.StopReason == "error" {
				entries = append(entries, session.Entry{Kind: session.KindStop, Reason: "error", Time: ts})
			}
		case "compaction_checkpoint":
			var c acpCompaction
			if json.Unmarshal(raw, &c) != nil {
				continue
			}
			flush()
			entries = append(entries, session.Entry{Kind: session.KindCompact, Text: checkpointSummary(src.Path, c.CheckpointFile), Time: ts})
		default:
			if acpIgnored[kind.SessionUpdate] {
				continue
			}
			unknown.Add(kind.SessionUpdate)
		}
		if stopAfterUser && gotUser {
			break
		}
	}
	if err := sc.Err(); err != nil {
		warnings = append(warnings, session.ReadStopWarning(err))
	}
	flush()
	return session.Thread{Source: src, Entries: entries, Warnings: unknown.AppendTo(warnings)}, nil
}

// lineTime picks the millisecond wall-clock when present, else the enclosing
// line's epoch-second timestamp.
func lineTime(ms, secs int64) time.Time {
	if ms != 0 {
		return time.UnixMilli(ms)
	}
	if secs != 0 {
		return time.Unix(secs, 0)
	}
	return time.Time{}
}

// hideBool reads hideFromScrollback, which Grok has written both as a JSON
// boolean and as the string "True".
func hideBool(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "true" || strings.EqualFold(s, `"true"`)
}

// textArray joins the text of a content block array, the shape tool_call_update
// carries: blocks are {"type":"text","text"} or a {"type":"content",
// "content":{"type":"text","text"}} wrapper.
func textArray(raw json.RawMessage) string {
	if len(raw) == 0 || raw[0] != '[' {
		return ""
	}
	var blocks []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Content *struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Content != nil && b.Content.Text != "" {
			parts = append(parts, b.Content.Text)
		} else if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolFailureText reads a failed call's output: the content blocks first, then
// rawOutput, which is a plain string or an object with a nested text field.
func toolFailureText(content, rawOutput json.RawMessage) string {
	if s := textArray(content); s != "" {
		return s
	}
	if s := rawOutputText(rawOutput); s != "" {
		return s
	}
	return "tool call failed"
}

func rawOutputText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return string(raw)
	}
	for _, key := range []string{"content", "output", "text", "stdout", "error", "message"} {
		if v, ok := obj[key]; ok {
			var inner string
			if json.Unmarshal(v, &inner) == nil && strings.TrimSpace(inner) != "" {
				return inner
			}
		}
	}
	return string(raw)
}

// checkpointSummary reads the compaction summary Grok saved for a seam, so
// --since-compact and a bare read show what replaced the context rather than an
// empty marker. It is a best effort: a missing or reshaped file leaves the
// marker bare.
func checkpointSummary(sessionDir, file string) string {
	if file == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(sessionDir, file))
	if err != nil {
		return ""
	}
	var c struct {
		CompactedHistory []struct {
			Type            string          `json:"type"`
			Content         json.RawMessage `json:"content"`
			SyntheticReason *string         `json:"synthetic_reason"`
		} `json:"compacted_history"`
	}
	if json.Unmarshal(b, &c) != nil {
		return ""
	}
	for _, it := range c.CompactedHistory {
		if it.Type == "user" && it.SyntheticReason != nil && *it.SyntheticReason == "compaction_meta" {
			return textArray(it.Content)
		}
	}
	return ""
}

// compactRaw preserves a structured tool input as compact JSON, or "" when
// absent.
func compactRaw(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return ""
	}
	return buf.String()
}

// --- fallback timeline (chat_history.jsonl) ---------------------------------

// grokUser decodes a user record's content blocks and its synthetic marker.
type grokUser struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	SyntheticReason *string `json:"synthetic_reason"`
}

// grokAssistant decodes an assistant record. content is a plain string, empty
// on a tool-only row.
type grokAssistant struct {
	Content   string `json:"content"`
	ToolCalls []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"tool_calls"`
	ModelID string `json:"model_id"`
}

// grokToolResult decodes a tool result row. A single call can produce several
// rows, all sharing tool_call_id.
type grokToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

// toolCall is the name and structured input of an assistant tool call.
type toolCall struct {
	name  string
	input string
}

// readChat parses chat_history.jsonl. It is the fallback for a session with no
// (or an oversized) updates.jsonl, and the listing preview: it is lossy (no
// timestamps, no error flags) but small. A missing transcript is not an error.
func readChat(src session.Source, path string) (session.Thread, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return session.Thread{Source: src}, nil
	}
	if err != nil {
		return session.Thread{}, err
	}
	defer f.Close()

	var entries []session.Entry
	var warnings []string
	var unknown session.UnknownTypes
	model := ""

	dec := json.NewDecoder(f)
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			warnings = append(warnings, session.ReadStopWarning(err))
			break
		}
		var rec struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		switch rec.Type {
		case "user":
			var u grokUser
			if json.Unmarshal(raw, &u) != nil {
				continue
			}
			text := userText(u)
			if u.SyntheticReason != nil {
				if *u.SyntheticReason == "compaction_meta" {
					markRetained(entries)
					entries = append(entries, session.Entry{Kind: session.KindCompact, Text: text})
				}
				continue
			}
			if q := extractUserQuery(text); q != "" {
				entries = append(entries, session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: q})
			}
		case "assistant":
			var a grokAssistant
			if json.Unmarshal(raw, &a) != nil {
				continue
			}
			if a.ModelID != "" {
				model = a.ModelID
			}
			if strings.TrimSpace(a.Content) != "" {
				entries = append(entries, session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: a.Content})
			}
		case "system", "reasoning", "tool_result", "backend_tool_call", "custom_tool_output":
			// The system prompt, model scratch work, and tool plumbing.
		default:
			unknown.Add(rec.Type)
		}
	}

	if model != "" {
		if src.Metadata == nil {
			src.Metadata = map[string]string{}
		}
		src.Metadata["model"] = model
	}
	return session.Thread{Source: src, Entries: entries, Warnings: unknown.AppendTo(warnings)}, nil
}

// userText joins a user record's text blocks with newlines; image blocks carry
// no text.
func userText(u grokUser) string {
	var parts []string
	for _, b := range u.Content {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

const (
	userQueryOpen  = "<user_query>"
	userQueryClose = "</user_query>"
)

// extractUserQuery returns a real user turn's words. Grok wraps a prompt with
// injected context, the actual request inside <user_query>…</user_query>; when
// the tags are absent the whole text is returned (so this also serves the
// fallback reader, whose real user record may be the bare prompt).
func extractUserQuery(text string) string {
	i := strings.Index(text, userQueryOpen)
	if i < 0 {
		return strings.TrimSpace(text)
	}
	rest := text[i+len(userQueryOpen):]
	if j := strings.Index(rest, userQueryClose); j >= 0 {
		return strings.TrimSpace(rest[:j])
	}
	return strings.TrimSpace(rest)
}

// markRetained flags the messages already read as having survived the
// compaction about to be marked. Only the fallback reader needs it: chat_history
// is replaced at a compaction, so everything still in the file was handed back
// to the model, and --since-compact must keep it rather than cut it away. The
// updates.jsonl reader keeps the whole history, so it needs no marks.
func markRetained(entries []session.Entry) {
	for i := range entries {
		switch entries[i].Kind {
		case session.KindMessage, session.KindFailure:
			entries[i].Retained = true
		}
	}
}
