// Package codex implements session.Provider over Codex CLI history: rollout JSONL
// files under $CODEX_HOME (default ~/.codex).
//
// Format reference, source of truth for RolloutLine/RolloutItem and the record
// payloads — codex-rs/protocol/src/protocol.rs in the Codex repo:
// https://github.com/openai/codex/blob/main/codex-rs/protocol/src/protocol.rs
//
// Useful records: session_meta.payload.{id,cwd,timestamp,cli_version,
// model_provider} for metadata; response_item.payload with type=message and
// role user/assistant, content types input_text and output_text, for the
// timeline; top-level type=compacted and event_msg.payload.type=context_compacted
// as compaction markers — Codex writes both, in that order, for one compaction,
// so the latter is suppressed. The compacted record's replacement_history is
// the context it handed the model afterwards; the turns it names are marked
// Retained so --since-compact can keep them.
//
// A command that exited non-zero becomes a failure entry, read from the
// event_msg.item_completed record whose item.type is CommandExecution
// (exit_code, command, aggregated_output). Codex writes those from cli 0.147
// on; earlier rollouts record the outcome only as prose inside
// function_call_output, which is not a flag, so they yield nothing.
//
// Ignored by default: function_call, function_call_output, custom_tool_call,
// web_search_call, MCP/tool events, patches, token counts, rate limits, memory
// citations, encrypted reasoning, turn_context, base instructions, and
// developer-role messages. event_msg.agent_message is a fallback only when
// canonical response_item messages are absent.
package codex

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

// Provider reads Codex rollout files.
type Provider struct{}

// New returns a Codex provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	files, err := sessionFiles(roots.Codex)
	if err != nil {
		return session.Source{}, err
	}
	if len(files) == 0 {
		return session.Source{}, fmt.Errorf("codex: no sessions found under %s", roots.Codex)
	}
	if id == "" {
		return readMeta(files[0]) // files are newest-first
	}
	// The session id is embedded in the rollout filename, so use that as a cheap
	// prefilter, then confirm against the parsed session_meta.
	if src, ok := findByID(files, id, true); ok {
		return src, nil
	}
	if src, ok := findByID(files, id, false); ok {
		return src, nil
	}
	return session.Source{}, fmt.Errorf("codex: no session with id %q", id)
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Path == "" {
		return session.Thread{}, errors.New("codex: source has no path")
	}
	info, err := os.Stat(src.Path)
	if err != nil {
		return session.Thread{}, err
	}
	return readThread(fileInfo{path: src.Path, mod: info.ModTime()})
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	return listSessions(roots.Codex, opts.Query, opts.Cwd, opts.EffectiveLimit())
}

// --- file enumeration -------------------------------------------------------

type fileInfo struct {
	path string
	mod  time.Time
}

// sessionFiles returns every rollout file under <root>/sessions, newest first.
func sessionFiles(root string) ([]fileInfo, error) {
	dir := filepath.Join(root, "sessions")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var files []fileInfo
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil // tolerate unreadable entries; just skip them
		}
		if info, e := d.Info(); e == nil {
			files = append(files, fileInfo{path: p, mod: info.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	return files, err
}

func findByID(files []fileInfo, id string, requireNameHit bool) (session.Source, bool) {
	for _, fi := range files {
		if requireNameHit && !strings.Contains(filepath.Base(fi.path), id) {
			continue
		}
		src, err := readMeta(fi)
		if err == nil && src.Ref.SessionID == id {
			return src, true
		}
	}
	return session.Source{}, false
}

// --- listing ----------------------------------------------------------------

// listSessions walks files newest-first and collects up to limit summaries.
// When cwd is set, only sessions whose working directory matches exactly are
// included.
func listSessions(root, query, cwd string, limit int) ([]session.Summary, error) {
	files, err := sessionFiles(root)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	out := make([]session.Summary, 0, limit)
	for _, fi := range files {
		if len(out) >= limit {
			break
		}
		t, err := readThread(fi)
		if err != nil || len(t.Entries) == 0 {
			continue
		}
		if cwd != "" && !session.SameDir(t.Source.Metadata["cwd"], cwd) {
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

// --- parsing ----------------------------------------------------------------

type codexLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type codexMeta struct {
	ID            string `json:"id"`
	Cwd           string `json:"cwd"`
	CliVersion    string `json:"cli_version"`
	ModelProvider string `json:"model_provider"`
}

type codexMessage struct {
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// codexCompaction is the payload of a top-level compacted record: the history
// Codex gave the model in place of the session so far. Items are ordinary
// message items plus one opaque encrypted item holding the model's summary.
type codexCompaction struct {
	Message            string             `json:"message"`
	ReplacementHistory []codexReplacement `json:"replacement_history"`
}

type codexReplacement struct {
	Type string `json:"type"`
	codexMessage
}

type codexEvent struct {
	Message string `json:"message"`
	Text    string `json:"text"`
}

// readMeta reads only the session_meta (first record) and returns a located
// Source without parsing the timeline.
func readMeta(fi fileInfo) (session.Source, error) {
	f, err := os.Open(fi.path)
	if err != nil {
		return session.Source{}, err
	}
	defer f.Close()

	src := newSource(fi)
	dec := json.NewDecoder(f)
	for dec.More() {
		var line codexLine
		if dec.Decode(&line) != nil {
			break
		}
		if line.Type == "session_meta" {
			var m codexMeta
			if json.Unmarshal(line.Payload, &m) == nil {
				applyMeta(&src, m)
			}
			break
		}
	}
	return src, nil
}

func readThread(fi fileInfo) (session.Thread, error) {
	f, err := os.Open(fi.path)
	if err != nil {
		return session.Thread{}, err
	}
	defer f.Close()

	src := newSource(fi)
	var entries, fallback []session.Entry
	var warnings []string
	var unknown session.UnknownTypes
	pendingCompactEvents := 0
	haveMessage := false

	dec := json.NewDecoder(f)
	for dec.More() {
		var line codexLine
		if err := dec.Decode(&line); err != nil {
			warnings = append(warnings, session.ReadStopWarning(err))
			break
		}
		ts := parseTime(line.Timestamp)

		switch line.Type {
		case "session_meta":
			var m codexMeta
			if json.Unmarshal(line.Payload, &m) == nil {
				applyMeta(&src, m)
			}

		case "compacted":
			marker, kept := compaction(line.Payload, ts)
			entries = append(entries, marker)
			pendingCompactEvents++
			markRetained(entries, kept)

		case "response_item":
			ptype := payloadType(line.Payload)
			switch ptype {
			case "message":
				var m codexMessage
				if json.Unmarshal(line.Payload, &m) != nil {
					continue
				}
				role := normalizeRole(m.Role)
				if role == "" {
					continue // developer/system/tool roles are dropped
				}
				text := joinContent(m)
				if text == "" {
					continue
				}
				if role == session.RoleUser && isInjectedUserText(text) {
					continue // environment/project-doc injection, not a typed turn
				}
				entries = append(entries, session.Entry{Kind: session.KindMessage, Role: role, Text: text, Time: ts})
				haveMessage = true
			case "agent_message", "custom_tool_call", "custom_tool_call_output", "function_call", "function_call_output",
				"reasoning", "tool_search_call", "tool_search_output", "web_search_call":
				// Model scratch work and tool plumbing represented elsewhere.
			default:
				unknown.Add("response_item/" + ptype)
			}

		case "event_msg":
			ptype := payloadType(line.Payload)
			switch ptype {
			case "context_compacted":
				if pendingCompactEvents > 0 {
					pendingCompactEvents--
				} else {
					entries = append(entries, session.Entry{Kind: session.KindCompact, Time: ts})
				}
			case "user_message":
				if e := decodeEvent(line.Payload); e != "" {
					fallback = append(fallback, session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: e, Time: ts})
				}
			case "agent_message":
				if e := decodeEvent(line.Payload); e != "" {
					fallback = append(fallback, session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: e, Time: ts})
				}
			case "item_completed":
				if e, ok := failedCommand(line.Payload, ts); ok {
					entries = append(entries, e)
				}
			case "agent_reasoning", "entered_review_mode", "exec_command_end", "exited_review_mode", "image_generation_end",
				"mcp_tool_call_end", "patch_apply_end", "sub_agent_activity", "task_complete", "task_started",
				"thread_rolled_back", "thread_settings_applied", "token_count", "turn_aborted", "web_search_end":
				// UI state, progress, and duplicate projections of response items.
			default:
				unknown.Add("event_msg/" + ptype)
			}

		case "turn_context", "world_state", "inter_agent_communication_metadata":
			// Per-turn settings, the workspace snapshot, and sub-agent routing:
			// written beside the conversation, never part of it.

		default:
			unknown.Add(line.Type)
		}
	}

	if !haveMessage {
		entries = fallback // older sessions only recorded event_msg messages
	}
	return session.Thread{Source: src, Entries: entries, Warnings: unknown.AppendTo(warnings)}, nil
}

// compaction reads a rollout compaction record: the summary Codex kept, and
// the history it handed the model in place of everything before the seam. The
// summary field is there but has always been empty in practice — the model's
// own recap travels inside the encrypted item that closes the replacement
// history — so those replaced turns are the only readable account of what
// survived, which is why they are worth recovering.
func compaction(raw json.RawMessage, ts time.Time) (session.Entry, []session.Entry) {
	var c codexCompaction
	if json.Unmarshal(raw, &c) != nil {
		return session.Entry{Kind: session.KindCompact, Time: ts}, nil
	}
	var kept []session.Entry
	for _, item := range c.ReplacementHistory {
		if item.Type != "message" {
			continue // the encrypted compaction item, and anything new
		}
		role := normalizeRole(item.Role)
		if role == "" {
			continue // developer-role scaffolding, as on the timeline itself
		}
		text := joinContent(item.codexMessage)
		if text == "" || (role == session.RoleUser && isInjectedUserText(text)) {
			continue
		}
		kept = append(kept, session.Entry{Kind: session.KindMessage, Role: role, Text: text})
	}
	return session.Entry{Kind: session.KindCompact, Text: c.Message, Time: ts}, kept
}

// markRetained flags the entries a compaction handed back to the model, so
// --since-compact keeps them. The replacement history repeats them verbatim
// and in order, so one forward walk pairs them off; a repeat that matches
// nothing is skipped rather than resynchronizing onto the wrong turn. Marks
// from an earlier compaction are cleared first: each record lists everything
// kept from every window before it, so the last one is the whole truth.
func markRetained(entries []session.Entry, kept []session.Entry) {
	for i := range entries {
		entries[i].Retained = false
	}
	at := 0
	for _, k := range kept {
		for i := at; i < len(entries); i++ {
			e := entries[i]
			if e.Kind == session.KindMessage && e.Role == k.Role && e.Text == k.Text {
				entries[i].Retained = true
				at = i + 1
				break
			}
		}
	}
}

func newSource(fi fileInfo) session.Source {
	return session.Source{
		Ref:       session.Ref{Provider: session.ProviderCodex},
		Path:      fi.path,
		UpdatedAt: fi.mod,
		Metadata:  map[string]string{},
	}
}

func applyMeta(src *session.Source, m codexMeta) {
	if m.ID != "" {
		src.Ref.SessionID = m.ID
	}
	if m.Cwd != "" {
		src.Metadata["cwd"] = m.Cwd
		src.Metadata["title"] = filepath.Base(m.Cwd)
	}
	if m.CliVersion != "" {
		src.Metadata["cli_version"] = m.CliVersion
	}
	if m.ModelProvider != "" {
		src.Metadata["model_provider"] = m.ModelProvider
	}
}

func payloadType(raw json.RawMessage) string {
	var p struct {
		Type string `json:"type"`
	}
	json.Unmarshal(raw, &p)
	return p.Type
}

// failedCommand converts an item_completed event whose CommandExecution item
// exited non-zero. The entry is named after the item type — the function_call
// that started it carries a different id, so there is nothing to pair with —
// and retains the command argv as its structured input. Its text ends with the
// exit status, which the output alone omits.
func failedCommand(raw json.RawMessage, ts time.Time) (session.Entry, bool) {
	var p struct {
		Item struct {
			Type     string          `json:"type"`
			Command  json.RawMessage `json:"command"`
			ExitCode *int            `json:"exit_code"`
			Output   string          `json:"aggregated_output"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &p) != nil || p.Item.Type != "CommandExecution" {
		return session.Entry{}, false
	}
	if p.Item.ExitCode == nil || *p.Item.ExitCode == 0 {
		return session.Entry{}, false
	}
	text := strings.TrimRight(p.Item.Output, "\n")
	if text != "" {
		text += "\n"
	}
	text += fmt.Sprintf("exit status %d", *p.Item.ExitCode)
	return session.Failure(p.Item.Type, p.Item.Command, text, ts), true
}

func decodeEvent(raw json.RawMessage) string {
	var e codexEvent
	json.Unmarshal(raw, &e)
	if e.Message != "" {
		return e.Message
	}
	return e.Text
}

func normalizeRole(role string) string {
	switch role {
	case session.RoleUser, session.RoleAssistant:
		return role
	default:
		return ""
	}
}

func joinContent(m codexMessage) string {
	var parts []string
	for _, c := range m.Content {
		if c.Text == "" {
			continue
		}
		switch c.Type {
		case "input_text", "output_text", "text":
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// injectionTags wrap the environment and project context Codex feeds in as
// user turns.
var injectionTags = []string{"INSTRUCTIONS", "skill", "user_instructions", "environment_context", "system-reminder", "recommended_plugins"}

// isInjectedUserText reports whether a user message is environment/context that
// Codex injects as a user turn rather than something the person typed: the
// project-doc preamble (e.g. "# AGENTS.md instructions for …"), or blocks
// wrapped in a known injection tag.
//
// One turn can carry several of these at once — a plugin catalog, then the
// project-doc preamble — so the test is that *nothing but* envelopes is there:
// strip them one by one and report true only when nothing a person wrote is
// left.
func isInjectedUserText(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	for {
		rest, ok := trimEnvelope(t)
		if !ok {
			return false
		}
		if t = strings.TrimSpace(rest); t == "" {
			return true
		}
	}
}

// trimEnvelope removes one leading injection envelope — a known tag block, or
// the heading line that introduces a project-doc <INSTRUCTIONS> block — and
// reports whether it found one.
func trimEnvelope(t string) (string, bool) {
	for _, tag := range injectionTags {
		open, closing := "<"+tag+">", "</"+tag+">"
		if !strings.HasPrefix(t, open) {
			continue
		}
		if i := strings.Index(t, closing); i >= 0 {
			return t[i+len(closing):], true
		}
	}
	// The preamble is a heading naming the doc ("# AGENTS.md instructions
	// for …", or just "# AGENTS.md instructions") ahead of its block; the
	// block itself is what makes the match safe.
	if strings.HasPrefix(t, "# ") && strings.Contains(firstLine(t), "instructions") {
		if rest := strings.TrimSpace(strings.TrimPrefix(t, firstLine(t))); strings.HasPrefix(rest, "<INSTRUCTIONS>") {
			return rest, true
		}
	}
	return "", false
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
