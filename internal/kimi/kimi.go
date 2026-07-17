// Package kimi implements session.Provider over Kimi Code CLI history: one
// directory per session under $KIMI_CODE_HOME/sessions (default ~/.kimi-code),
// holding a state.json metadata file and an event-sourced record log at
// agents/main/wire.jsonl. Subagents write sibling agents/<name>/ logs, so
// reading only main excludes them.
//
// Format reference, source of truth for the record shapes:
// https://github.com/MoonshotAI/kimi-code
// packages/agent-core/src/agent/records/ (the restore switch in index.ts).
//
// Useful records: context.append_message role user for prompts (records with
// origin.kind "injection" are system reminders, not the user); assistant text
// arrives as context.append_loop_event content.part records on wire protocol
// 1.4, and as context.append_message role assistant on 1.0 logs migrated from
// the legacy kimi-cli — both shapes are always accepted, no version branching.
// context.apply_compaction carries the compaction summary; config.update
// carries modelAlias. Session id, title, cwd, and timestamps come from
// state.json, so Resolve never opens the wire log.
//
// Ignored by default: turn.prompt (duplicates append_message), think parts,
// tool.call/tool.result events, usage, permission, and llm.* bookkeeping.
package kimi

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

// Provider reads Kimi Code CLI session directories.
type Provider struct{}

// New returns a Kimi provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	dirs, err := sessionDirs(roots.Kimi)
	if err != nil {
		return session.Source{}, err
	}
	if len(dirs) == 0 {
		return session.Source{}, fmt.Errorf("kimi: no sessions found under %s", roots.Kimi)
	}
	if id == "" {
		return newSource(dirs[0]), nil
	}
	for _, d := range dirs {
		if filepath.Base(d.path) == id {
			return newSource(d), nil
		}
	}
	return session.Source{}, fmt.Errorf("kimi: no session with id %q", id)
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Path == "" {
		return session.Thread{}, errors.New("kimi: source has no path")
	}
	return readThread(src)
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	dirs, err := sessionDirs(roots.Kimi)
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
		// state.json answers the cwd filter without opening the wire log.
		if opts.Cwd != "" && d.meta.WorkDir != opts.Cwd {
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
	meta stateJSON
	mod  time.Time
}

// stateJSON is the cheap per-session metadata kimi maintains beside the wire
// log. The session id is not in it; the directory name is the id.
type stateJSON struct {
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
	Title     string `json:"title"`
	WorkDir   string `json:"workDir"`
}

// sessionDirs returns every session directory under <root>/sessions, newest
// first. The layout is sessions/wd_<slug>/<sessionId>/state.json; kimi also
// keeps a session_index.jsonl at the root, but it is an append-only
// accelerator with stale rows, so the directory tree stays the single source
// of truth here.
func sessionDirs(root string) ([]sessionDir, error) {
	base := filepath.Join(root, "sessions")
	wds, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dirs []sessionDir
	for _, wd := range wds {
		if !wd.IsDir() {
			continue
		}
		ents, err := os.ReadDir(filepath.Join(base, wd.Name()))
		if err != nil {
			continue
		}
		for _, ent := range ents {
			if !ent.IsDir() {
				continue
			}
			path := filepath.Join(base, wd.Name(), ent.Name())
			d, ok := readState(path)
			if !ok {
				continue
			}
			dirs = append(dirs, d)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
	return dirs, nil
}

func readState(dir string) (sessionDir, bool) {
	path := filepath.Join(dir, "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return sessionDir{}, false
	}
	var meta stateJSON
	if json.Unmarshal(raw, &meta) != nil {
		return sessionDir{}, false
	}
	mod := parseTime(meta.UpdatedAt)
	if mod.IsZero() {
		if info, err := os.Stat(path); err == nil {
			mod = info.ModTime()
		}
	}
	return sessionDir{path: dir, meta: meta, mod: mod}, true
}

func newSource(d sessionDir) session.Source {
	meta := map[string]string{}
	if d.meta.Title != "" {
		meta["title"] = strings.TrimSpace(d.meta.Title)
	}
	if d.meta.WorkDir != "" {
		meta["cwd"] = d.meta.WorkDir
	}
	return session.Source{
		Ref:       session.Ref{Provider: session.ProviderKimi, SessionID: filepath.Base(d.path)},
		Path:      d.path,
		UpdatedAt: d.mod,
		Metadata:  meta,
	}
}

// --- parsing ----------------------------------------------------------------

type wireRecord struct {
	Type       string       `json:"type"`
	Time       int64        `json:"time"` // ms epoch; absent on 1.0 records
	Message    *wireMessage `json:"message"`
	Event      *wireEvent   `json:"event"`
	ModelAlias string       `json:"modelAlias"` // config.update
	Summary    string       `json:"summary"`    // context.apply_compaction
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wirePart  `json:"content"`
	Origin  *wireOrigin `json:"origin"`
}

type wireOrigin struct {
	Kind string `json:"kind"`
}

type wireEvent struct {
	Type string    `json:"type"`
	Part *wirePart `json:"part"`
}

type wirePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func readThread(src session.Source) (session.Thread, error) {
	f, err := os.Open(filepath.Join(src.Path, "agents", "main", "wire.jsonl"))
	if err != nil {
		return session.Thread{}, err
	}
	defer f.Close()

	var entries []session.Entry
	var warnings []string
	dec := json.NewDecoder(f)
	for dec.More() {
		var rec wireRecord
		if dec.Decode(&rec) != nil {
			warnings = append(warnings, "stopped reading at a malformed record")
			break
		}
		switch rec.Type {
		case "context.append_message":
			if e, ok := messageEntry(rec); ok {
				entries = append(entries, e)
			}
		case "context.append_loop_event":
			if e, ok := loopEntry(rec); ok {
				entries = append(entries, e)
			}
		case "context.apply_compaction":
			entries = append(entries, session.Entry{Kind: session.KindCompact, Text: rec.Summary, Time: recTime(rec)})
		case "config.update":
			if rec.ModelAlias != "" {
				src.Metadata["model"] = rec.ModelAlias
			}
		}
	}
	return session.Thread{Source: src, Entries: entries, Warnings: warnings}, nil
}

// messageEntry converts a context.append_message record. User records with
// origin.kind "injection" are system reminders kimi appends as fake user
// turns; 1.0 records predate origin, so those fall back to the reminder's
// text marker. Assistant records only occur on 1.0 logs (1.4 streams
// assistant text as loop events); tool-role records are skipped.
func messageEntry(rec wireRecord) (session.Entry, bool) {
	m := rec.Message
	if m == nil {
		return session.Entry{}, false
	}
	role := m.Role
	if role != session.RoleUser && role != session.RoleAssistant {
		return session.Entry{}, false
	}
	if role == session.RoleUser {
		if m.Origin != nil && m.Origin.Kind != "user" {
			return session.Entry{}, false
		}
	}
	text := joinText(m.Content)
	if text == "" || (m.Origin == nil && strings.HasPrefix(text, "<system-reminder>")) {
		return session.Entry{}, false
	}
	return session.Entry{Kind: session.KindMessage, Role: role, Text: text, Time: recTime(rec)}, true
}

// loopEntry converts a context.append_loop_event record: content.part events
// with a text part are the assistant's visible output on 1.4 logs. Think
// parts and tool.call/tool.result events never reach the timeline.
func loopEntry(rec wireRecord) (session.Entry, bool) {
	ev := rec.Event
	if ev == nil || ev.Type != "content.part" || ev.Part == nil || ev.Part.Type != "text" || ev.Part.Text == "" {
		return session.Entry{}, false
	}
	return session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: ev.Part.Text, Time: recTime(rec)}, true
}

func joinText(parts []wirePart) string {
	var out []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

func recTime(rec wireRecord) time.Time {
	if rec.Time <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(rec.Time)
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
