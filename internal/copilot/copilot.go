// Package copilot implements session.Provider over GitHub Copilot CLI
// history: one directory per session under $COPILOT_HOME/session-state
// (default ~/.copilot/session-state).
//
// Format reference, from a live install (@github/copilot 1.0.80) and the
// schemas/session-events.schema.json it ships:
//
//	session-state/<uuid>/workspace.yaml holds the session's metadata as a
//	flat key/value map (id, cwd, name, created_at, updated_at, plus git_root,
//	repository and branch when the session started inside a repository), and
//	session-state/<uuid>/events.jsonl is the append-only event log. Resuming
//	a session appends to the same log rather than starting a new directory,
//	so one session is always one file. The directory name is the session id:
//	the value --resume takes, and the id every listing reports.
//
// Every event is {type, id, parentId, timestamp, agentId, data} with an
// RFC 3339 timestamp. Visible on the timeline: user/message content
// (data.content is what the human typed; data.transformedContent is the same
// text wrapped in injected datetime and system-reminder context, and is not
// conversation) and assistant/message content, which is empty on the turns
// that only carry tool requests. A tool.execution_complete with success
// false is a failure entry, paired through toolCallId with its
// tool.execution_start for the tool's name and arguments; its text is
// error.message, or result.content when no error is recorded. Everything
// else — system.message, the rest of tool.*, assistant.turn_*,
// session.usage_checkpoint, and the session lifecycle events — is
// bookkeeping.
//
// Sub-agent traffic reuses those same two types and is told apart by the
// envelope's agentId, which the schema documents as "absent for events from
// the root/main agent". A sub-agent's
// prompts and answers are the parent turn's tool plumbing, not conversation,
// so any event carrying an agentId is skipped — including for the model,
// which a sub-agent routed to another model would otherwise overwrite.
//
// A session.compaction_complete carries success and, when the compaction
// succeeded, summaryContent: the LLM-written summary that replaced the
// history. It becomes the compaction marker's text, so --since-compact opens
// on what survived rather than on a bare seam. A failed compaction removed
// nothing and is not a seam, so it produces no marker.
//
// The model is read from each root assistant message's data.model, last
// writer wins: sessions run with --model auto are routed per turn, so the last
// answer's model is the one that produced the tail of the transcript.
//
// Session recency comes from the event log's mtime, not workspace.yaml's
// updated_at: the yaml is rewritten when the session's metadata changes
// (naming, resume), so it lags a session that is still appending events.
//
// Unverified: no local session compacted, spawned a sub-agent, or had a tool
// call fail, so the compaction, agentId, and failure handling follow the
// shipped schema rather than an observed log. Legacy sessions under history-session-state/ (pre-migration
// format) are not read; Copilot migrates one into session-state/ the first
// time it is resumed.
package copilot

import (
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

// Provider reads Copilot CLI session state. It is stateless; every call
// re-reads the files, so a concurrently writing copilot is never blocked.
type Provider struct{}

// New returns a GitHub Copilot CLI provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

const (
	eventsFile    = "events.jsonl"
	workspaceFile = "workspace.yaml"
)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	dirs, err := sessionDirs(roots.Copilot)
	if err != nil {
		return session.Source{}, err
	}
	if len(dirs) == 0 {
		return session.Source{}, fmt.Errorf("copilot: no sessions found under %s", roots.Copilot)
	}
	if id != "" {
		for _, d := range dirs {
			if filepath.Base(d.path) == id {
				return readMeta(d)
			}
		}
		return session.Source{}, fmt.Errorf("copilot: no session with id %q", id)
	}
	return readMeta(dirs[0])
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Path == "" {
		return session.Thread{}, errors.New("copilot: source has no path")
	}
	info, err := os.Stat(src.Path)
	if err != nil {
		return session.Thread{}, err
	}
	d := dirInfo{path: filepath.Dir(src.Path), mod: info.ModTime()}
	return readThread(d, readWorkspace(d.path))
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	dirs, err := sessionDirs(roots.Copilot)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(opts.Query)
	limit := opts.EffectiveLimit()
	out := make([]session.Summary, 0, limit)
	for _, d := range dirs {
		if len(out) >= limit {
			break
		}
		// The directory filter is answered from workspace.yaml alone, so a
		// session in another directory never costs an event-log parse.
		meta := readWorkspace(d.path)
		if opts.Cwd != "" && meta["cwd"] != opts.Cwd {
			continue
		}
		t, err := readThread(d, meta)
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

// --- directory enumeration --------------------------------------------------

// dirInfo is one session directory. No id field: the base name is the id, and
// a copy could only disagree with the path it came from.
type dirInfo struct {
	path string
	mod  time.Time
}

// sessionDirs returns every session directory under <root>/session-state that
// holds an event log, newest first. Only the event log is required, so a
// session whose workspace.yaml is missing or truncated is still listed.
func sessionDirs(root string) ([]dirInfo, error) {
	base := filepath.Join(root, "session-state")
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []dirInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		info, err := os.Stat(filepath.Join(dir, eventsFile))
		if err != nil {
			continue
		}
		dirs = append(dirs, dirInfo{path: dir, mod: info.ModTime()})
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
	return dirs, nil
}

// readMeta delegates instead of describing the session from workspace.yaml
// alone: the yaml has no model, and a metadata-only view that omits the model
// would be poorer than every other provider's. Event logs are small (a long
// session stays under a few MB).
func readMeta(d dirInfo) (session.Source, error) {
	t, err := readThread(d, readWorkspace(d.path))
	return t.Source, err
}

// readSource describes a session from its already-parsed workspace.yaml —
// everything a listing row needs except the timeline itself.
func readSource(d dirInfo, meta map[string]string) session.Source {
	src := session.Source{
		Ref:       session.Ref{Provider: session.ProviderCopilot, SessionID: filepath.Base(d.path)},
		Path:      filepath.Join(d.path, eventsFile),
		UpdatedAt: d.mod,
		Metadata:  map[string]string{},
	}
	if cwd := meta["cwd"]; cwd != "" {
		src.Metadata["cwd"] = cwd
	}
	if name := meta["name"]; name != "" {
		src.Metadata["title"] = name
	} else if cwd := meta["cwd"]; cwd != "" {
		src.Metadata["title"] = filepath.Base(cwd)
	}
	return src
}

// readWorkspace parses workspace.yaml. The file is a flat map of scalars
// written by Copilot itself — no nesting, no lists — so a full YAML parser
// would be a dependency bought for a dozen lines of text. A missing or
// unreadable file yields an empty map, leaving the session readable from its
// event log alone.
func readWorkspace(dir string) map[string]string {
	raw, err := os.ReadFile(filepath.Join(dir, workspaceFile))
	if err != nil {
		return map[string]string{}
	}
	meta := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		// Indented lines would be nested values; Copilot writes none, and
		// guessing at one's meaning is worse than ignoring it.
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		meta[strings.TrimSpace(key)] = unquote(strings.TrimSpace(val))
	}
	return meta
}

// unquote strips the one quoting form Copilot's writer emits: a single-quoted
// scalar, which escapes its own quote by doubling it. Bare scalars pass
// through untouched.
func unquote(v string) string {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}

// --- parsing ----------------------------------------------------------------

// The envelope decodes only the fields every event carries; data is kept raw
// and handed to the per-type shapes in applyEvent, because unrelated events
// reuse field names with different shapes and one mismatch would fail the
// whole event's decode.
type cpEvent struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	AgentID   string          `json:"agentId"`
	Data      json.RawMessage `json:"data"`
}

type cpUserMessage struct {
	Content string `json:"content"`
}

type cpAssistantMessage struct {
	Content string `json:"content"`
	Model   string `json:"model"`
}

type cpCompaction struct {
	Success bool   `json:"success"`
	Summary string `json:"summaryContent"`
}

type cpToolStart struct {
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Arguments  json.RawMessage `json:"arguments"`
}

// success is a pointer so that only an explicit false counts as a failure;
// result and error stay raw because the schema calls them objects and a
// mismatch must not cost the event.
type cpToolComplete struct {
	ToolCallID string          `json:"toolCallId"`
	Success    *bool           `json:"success"`
	Result     json.RawMessage `json:"result"`
	Error      json.RawMessage `json:"error"`
}

// toolCall is what a failure needs from the tool.execution_start it answers.
type toolCall struct {
	name string
	args json.RawMessage
}

func readThread(d dirInfo, meta map[string]string) (session.Thread, error) {
	src := readSource(d, meta)
	f, err := os.Open(src.Path)
	if err != nil {
		return session.Thread{}, err
	}
	defer f.Close()

	var entries []session.Entry
	var warnings []string
	calls := map[string]toolCall{} // toolCallId → call, until its result arrives
	dec := json.NewDecoder(f)
	for dec.More() {
		var ev cpEvent
		if err := dec.Decode(&ev); err != nil {
			// A killed writer can leave a torn final line; keep the prefix.
			warnings = append(warnings, "stopped reading at a malformed record")
			break
		}
		applyEvent(&src, &entries, calls, ev)
	}
	return session.Thread{Source: src, Entries: entries, Warnings: warnings}, nil
}

// applyEvent folds one event into the source metadata or the timeline. An
// unparseable or empty payload makes the event contribute nothing rather than
// fail the read.
func applyEvent(src *session.Source, entries *[]session.Entry, calls map[string]toolCall, ev cpEvent) {
	if ev.AgentID != "" {
		return // a sub-agent's own turns: the parent's tool plumbing
	}
	switch ev.Type {
	case "user.message":
		var d cpUserMessage
		if json.Unmarshal(ev.Data, &d) != nil || d.Content == "" {
			return
		}
		*entries = append(*entries, session.Entry{
			Kind: session.KindMessage, Role: session.RoleUser,
			Text: d.Content, Time: parseTime(ev.Timestamp),
		})
	case "assistant.message":
		var d cpAssistantMessage
		if json.Unmarshal(ev.Data, &d) != nil {
			return
		}
		if d.Model != "" {
			src.Metadata["model"] = d.Model
		}
		if d.Content == "" {
			return // a turn that only requested tools
		}
		*entries = append(*entries, session.Entry{
			Kind: session.KindMessage, Role: session.RoleAssistant,
			Text: d.Content, Time: parseTime(ev.Timestamp),
		})
	case "session.compaction_complete":
		var d cpCompaction
		if json.Unmarshal(ev.Data, &d) != nil || !d.Success {
			return // a failed compaction removed nothing: not a seam
		}
		*entries = append(*entries, session.Entry{
			Kind: session.KindCompact, Text: d.Summary, Time: parseTime(ev.Timestamp),
		})
	case "tool.execution_start":
		var d cpToolStart
		if json.Unmarshal(ev.Data, &d) == nil && d.ToolCallID != "" {
			calls[d.ToolCallID] = toolCall{name: d.ToolName, args: d.Arguments}
		}
	case "tool.execution_complete":
		var d cpToolComplete
		if json.Unmarshal(ev.Data, &d) != nil {
			return
		}
		call := calls[d.ToolCallID]
		delete(calls, d.ToolCallID)
		if d.Success == nil || *d.Success {
			return
		}
		text := stringField(d.Error, "message")
		if text == "" {
			text = stringField(d.Result, "content")
		}
		*entries = append(*entries, session.Failure(call.name, call.args, text, parseTime(ev.Timestamp)))
	}
}

// stringField is the string at key in a JSON object, or "" for anything else.
func stringField(raw json.RawMessage, key string) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return ""
	}
	var s string
	json.Unmarshal(obj[key], &s)
	return s
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
