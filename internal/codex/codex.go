// Package codex implements session.Provider over Codex CLI history: rollout JSONL
// files under $CODEX_HOME (default ~/.codex).
//
// Useful records: session_meta.payload.{id,cwd,timestamp,cli_version,
// model_provider} for metadata; response_item.payload with type=message and
// role user/assistant, content types input_text and output_text, for the
// timeline; type=compacted and event_msg.payload.type=context_compacted as
// compaction markers.
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

	"github.com/wilbeibi/baton/internal/session"
)

const defaultLimit = 20

// Provider reads Codex rollout files. It holds no state today; the receiver is
// kept so per-instance configuration (clock, fs override) can be added without
// changing the constructor's callers.
type Provider struct{}

// New returns a Codex provider.
func New() *Provider { return &Provider{} }

// Ensure Provider satisfies the interface at compile time.
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

func (p *Provider) ResolveRank(ctx context.Context, roots session.Roots, opts session.ListOptions, rank int) (session.Source, error) {
	if rank < 1 {
		return session.Source{}, fmt.Errorf("codex: rank must be >= 1")
	}
	sums, err := listSessions(roots.Codex, opts.Query, rank)
	if err != nil {
		return session.Source{}, err
	}
	if rank > len(sums) {
		return session.Source{}, fmt.Errorf("codex: rank %d out of range (%d matching sessions)", rank, len(sums))
	}
	return p.Resolve(ctx, roots, sums[rank-1].Ref.SessionID)
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
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	return listSessions(roots.Codex, opts.Query, limit)
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
// Without a query it reads only the first limit files; with a query it reads in
// recency order until limit matches are found.
func listSessions(root, query string, limit int) ([]session.Summary, error) {
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
		if q != "" && !strings.Contains(strings.ToLower(visibleText(t)), q) {
			continue
		}
		out = append(out, summaryOf(t))
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, nil
}

func summaryOf(t session.Thread) session.Summary {
	return session.Summary{
		Ref:       t.Source.Ref,
		UpdatedAt: t.Source.UpdatedAt,
		Title:     t.Source.Metadata["title"],
		Cwd:       t.Source.Metadata["cwd"],
		Preview:   preview(t),
	}
}

func preview(t session.Thread) string {
	for _, e := range t.Entries {
		if e.Kind == session.KindMessage && e.Role == session.RoleUser && e.Text != "" {
			return e.Text
		}
	}
	for _, e := range t.Entries {
		if e.Text != "" {
			return e.Text
		}
	}
	return ""
}

func visibleText(t session.Thread) string {
	var b strings.Builder
	for _, e := range t.Entries {
		b.WriteString(e.Text)
		b.WriteByte('\n')
	}
	return b.String()
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

// readThread parses a rollout file into a visible timeline.
func readThread(fi fileInfo) (session.Thread, error) {
	f, err := os.Open(fi.path)
	if err != nil {
		return session.Thread{}, err
	}
	defer f.Close()

	src := newSource(fi)
	var entries, fallback []session.Entry
	var warnings []string
	haveMessage := false

	dec := json.NewDecoder(f)
	for dec.More() {
		var line codexLine
		if dec.Decode(&line) != nil {
			warnings = append(warnings, "skipped a malformed record")
			break
		}
		ts := parseTime(line.Timestamp)

		switch line.Type {
		case "session_meta":
			var m codexMeta
			if json.Unmarshal(line.Payload, &m) == nil {
				applyMeta(&src, m)
			}

		case "response_item":
			switch payloadType(line.Payload) {
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
			case "compacted":
				entries = append(entries, session.Entry{Kind: session.KindCompact, Time: ts})
			}

		case "event_msg":
			switch payloadType(line.Payload) {
			case "context_compacted":
				entries = append(entries, session.Entry{Kind: session.KindCompact, Time: ts})
			case "user_message":
				if e := decodeEvent(line.Payload); e != "" {
					fallback = append(fallback, session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: e, Time: ts})
				}
			case "agent_message":
				if e := decodeEvent(line.Payload); e != "" {
					fallback = append(fallback, session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: e, Time: ts})
				}
			}
		}
	}

	if !haveMessage {
		entries = fallback // older sessions only recorded event_msg messages
	}
	return session.Thread{Source: src, Entries: entries, Warnings: warnings}, nil
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

// isInjectedUserText reports whether a user message is environment/context that
// Codex injects as a user turn rather than something the person typed: the
// project-doc preamble (e.g. "# AGENTS.md instructions for …"), or a block
// wholly wrapped in a known injection tag. It deliberately matches only the
// whole-message envelope so real prose is never dropped.
func isInjectedUserText(s string) bool {
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "# ") && strings.Contains(firstLine(t), " instructions for ") && strings.Contains(t, "<INSTRUCTIONS>") {
		return true
	}
	for _, tag := range []string{"INSTRUCTIONS", "skill", "user_instructions", "environment_context", "system-reminder"} {
		if strings.HasPrefix(t, "<"+tag+">") && strings.HasSuffix(t, "</"+tag+">") {
			return true
		}
	}
	return false
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
