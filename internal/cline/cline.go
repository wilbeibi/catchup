// Package cline implements session.Provider over Cline CLI history: one
// directory per session under $CLINE_DIR/data/sessions (default ~/.cline),
// holding a <id>.json manifest and the transcript in <id>.messages.json.
// Subagent transcripts share the directory under other file stems, so reading
// exactly <id>.messages.json excludes them.
//
// Format reference, source of truth for the shapes:
// https://github.com/cline/cline — apps/cli/src/session/,
// sdk/packages/core/src/services/session-artifacts.ts, and
// sdk/packages/shared/src/llms/messages.ts (MessageWithMetadata).
//
// Useful shapes: the manifest carries session_id, cwd, provider, model,
// prompt, metadata.title, and started_at/ended_at; messages are
// Anthropic-style role user/assistant with string-or-blocks content. Real user
// input arrives wrapped in a <user_input mode="..."> tag, which is stripped;
// user messages holding only tool_result blocks fall out naturally because
// only text blocks reach the timeline. Compaction folds history into a user
// message with metadata.kind "compaction_summary", whose metadata.summary is
// the summary text.
//
// Ignored by default: thinking and tool_use blocks, tool_result content,
// system_prompt, usage metrics, and sibling subagent transcripts.
package cline

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

// Provider reads Cline CLI session directories.
type Provider struct{}

// New returns a Cline provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	dirs, err := sessionDirs(roots.Cline)
	if err != nil {
		return session.Source{}, err
	}
	if len(dirs) == 0 {
		return session.Source{}, fmt.Errorf("cline: no sessions found under %s", roots.Cline)
	}
	if id == "" {
		return newSource(dirs[0]), nil
	}
	for _, d := range dirs {
		if d.meta.SessionID == id {
			return newSource(d), nil
		}
	}
	return session.Source{}, fmt.Errorf("cline: no session with id %q", id)
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Path == "" {
		return session.Thread{}, errors.New("cline: source has no path")
	}
	return readThread(src)
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	dirs, err := sessionDirs(roots.Cline)
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
		// The manifest answers the cwd filter without opening the transcript.
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

type sessionDir struct {
	path string // the session directory itself
	meta manifest
	mod  time.Time
}

// manifest is the cheap per-session metadata cline writes beside the
// transcript, named <session_id>.json.
type manifest struct {
	SessionID string `json:"session_id"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Cwd       string `json:"cwd"`
	Prompt    string `json:"prompt"`
	Metadata  struct {
		Title string `json:"title"`
	} `json:"metadata"`
}

// sessionDirs returns every session directory under <root>/data/sessions,
// newest first. Recency comes from the transcript's mtime — it advances with
// every message, where the manifest's ended_at stays null while a session
// runs — falling back to the manifest timestamps for transcript-less dirs.
func sessionDirs(root string) ([]sessionDir, error) {
	base := filepath.Join(root, "data", "sessions")
	ents, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []sessionDir
	for _, ent := range ents {
		if !ent.IsDir() {
			continue
		}
		path := filepath.Join(base, ent.Name())
		d, ok := readManifest(path, ent.Name())
		if !ok {
			continue
		}
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
	return dirs, nil
}

func readManifest(dir, name string) (sessionDir, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		return sessionDir{}, false
	}
	var meta manifest
	if json.Unmarshal(raw, &meta) != nil || meta.SessionID == "" {
		return sessionDir{}, false
	}
	mod := time.Time{}
	if info, err := os.Stat(filepath.Join(dir, name+".messages.json")); err == nil {
		mod = info.ModTime()
	}
	if mod.IsZero() {
		mod = parseTime(meta.EndedAt)
	}
	if mod.IsZero() {
		mod = parseTime(meta.StartedAt)
	}
	return sessionDir{path: dir, meta: meta, mod: mod}, true
}

func newSource(d sessionDir) session.Source {
	meta := map[string]string{}
	title := strings.TrimSpace(d.meta.Metadata.Title)
	if title == "" {
		title = strings.TrimSpace(d.meta.Prompt)
	}
	if title != "" {
		meta["title"] = title
	}
	if d.meta.Cwd != "" {
		meta["cwd"] = d.meta.Cwd
	}
	if d.meta.Model != "" {
		meta["model"] = d.meta.Model
	}
	if d.meta.Provider != "" {
		meta["model_provider"] = d.meta.Provider
	}
	return session.Source{
		Ref:       session.Ref{Provider: session.ProviderCline, SessionID: d.meta.SessionID},
		Path:      d.path,
		UpdatedAt: d.mod,
		Metadata:  meta,
	}
}

// --- parsing ----------------------------------------------------------------

// messagesFile is the transcript envelope of <session_id>.messages.json.
type messagesFile struct {
	Messages []clineMessage `json:"messages"`
}

type clineMessage struct {
	Role     string          `json:"role"`
	Content  json.RawMessage `json:"content"` // string or []block
	Metadata *struct {
		Kind    string `json:"kind"`
		Summary string `json:"summary"`
	} `json:"metadata"`
	Ts int64 `json:"ts"`
}

type block struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func readThread(src session.Source) (session.Thread, error) {
	raw, err := os.ReadFile(filepath.Join(src.Path, src.Ref.SessionID+".messages.json"))
	if err != nil {
		return session.Thread{}, err
	}
	var file messagesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return session.Thread{}, fmt.Errorf("cline: malformed transcript: %w", err)
	}
	var entries []session.Entry
	for _, m := range file.Messages {
		if e, ok := messageEntry(m); ok {
			entries = append(entries, e)
		}
	}
	return session.Thread{Source: src, Entries: entries}, nil
}

func messageEntry(m clineMessage) (session.Entry, bool) {
	ts := time.Time{}
	if m.Ts > 0 {
		ts = time.UnixMilli(m.Ts)
	}
	if m.Role == session.RoleUser && m.Metadata != nil && m.Metadata.Kind == "compaction_summary" {
		text := m.Metadata.Summary
		if text == "" {
			text = extractText(m.Content)
		}
		return session.Entry{Kind: session.KindCompact, Text: text, Time: ts}, true
	}
	if m.Role != session.RoleUser && m.Role != session.RoleAssistant {
		return session.Entry{}, false
	}
	text := extractText(m.Content)
	if m.Role == session.RoleUser {
		text = unwrapUserInput(text)
	}
	if text == "" {
		return session.Entry{}, false
	}
	return session.Entry{Kind: session.KindMessage, Role: m.Role, Text: text, Time: ts}, true
}

// unwrapUserInput strips the <user_input mode="..."> envelope cline wraps
// around what the user actually typed. Text without the envelope (older
// sessions, piped input variants) passes through unchanged.
func unwrapUserInput(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "<user_input") {
		return text
	}
	open := strings.Index(trimmed, ">")
	if open < 0 {
		return text
	}
	inner := trimmed[open+1:]
	inner = strings.TrimSuffix(strings.TrimSpace(inner), "</user_input>")
	return strings.TrimSpace(inner)
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
