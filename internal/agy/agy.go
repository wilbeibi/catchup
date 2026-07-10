// Package agy implements session.Provider over Antigravity CLI history:
// step transcripts under
// <root>/brain/<conversation-id>/.system_generated/logs/transcript.jsonl
// (default ~/.gemini/antigravity-cli; no override variable is known).
//
// Antigravity's primary conversation store (conversations/*.pb, *.db) is an
// undocumented protobuf/SQLite pairing; the brain transcript is the one
// plain-text record the CLI writes, so this provider reads that. Each line is
// one step: {step_index, source, type, status, created_at, content}.
//
// Useful steps: USER_INPUT (the user turn, wrapped in <USER_REQUEST> tags),
// PLANNER_RESPONSE from MODEL (the assistant's prose), and CHECKPOINT (the
// CLI's own truncation summary, surfaced as a compaction marker). Everything
// else — RUN_COMMAND, VIEW_FILE, CODE_ACTION and the other tool steps,
// EPHEMERAL_MESSAGE / SYSTEM_MESSAGE / CONVERSATION_HISTORY bookkeeping — is
// skipped.
//
// Subagent conversations write brain transcripts indistinguishable from
// top-level ones. The CLI's <root>/history.jsonl, however, records one line
// per user-typed prompt ({display, workspace, timestamp, conversationId}), so
// it doubles as the directory index — and keeps subagents out of cwd-filtered
// listings. One wrinkle, verified against real stores: a conversation's first
// prompt is logged before its id exists, so that line has no conversationId
// and single-prompt conversations never get one. Those are joined back to
// their transcript by matching the first user turn's text (closest timestamp
// on ties); see history.matchStart.
package agy

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

// Provider reads Antigravity CLI brain transcripts.
type Provider struct{}

// New returns an Antigravity provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	// The transcript path is a pure function of the conversation id, so an
	// explicit id needs no enumeration — which is also what keeps conversations
	// too old for history.jsonl reachable.
	if id != "" {
		fi, err := transcriptOf(roots.Agy, id)
		if err != nil {
			return session.Source{}, fmt.Errorf("agy: no session with id %q", id)
		}
		return resolveSource(fi, roots.Agy), nil
	}
	files, err := sessionFiles(roots.Agy)
	if err != nil {
		return session.Source{}, err
	}
	if len(files) == 0 {
		return session.Source{}, fmt.Errorf("agy: no sessions found under %s", roots.Agy)
	}
	return resolveSource(files[0], roots.Agy), nil
}

// resolveSource builds a Source for one transcript, paying one extra
// transcript read for the matchStart join when history.jsonl has no id line
// for it (single-prompt conversations).
func resolveSource(fi fileInfo, root string) session.Source {
	h := loadHistory(root)
	src := sourceOf(fi, h)
	if src.Metadata["cwd"] == "" {
		if entries, _, err := readEntries(fi); err == nil {
			applyStart(&src, entries, h)
		}
	}
	return src
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Path == "" {
		return session.Thread{}, errors.New("agy: source has no path")
	}
	info, err := os.Stat(src.Path)
	if err != nil {
		return session.Thread{}, err
	}
	fi := fileInfo{path: src.Path, mod: info.ModTime(), id: src.Ref.SessionID}
	entries, warnings, err := readEntries(fi)
	if err != nil {
		return session.Thread{}, err
	}
	return session.Thread{Source: src, Entries: entries, Warnings: warnings}, nil
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	return listSessions(roots.Agy, opts.Query, opts.Cwd, opts.EffectiveLimit())
}

// --- file enumeration -------------------------------------------------------

type fileInfo struct {
	path string
	mod  time.Time
	id   string // conversation id, the brain directory's name
}

// transcriptOf stats the brain transcript belonging to one conversation id.
func transcriptOf(root, id string) (fileInfo, error) {
	p := filepath.Join(root, "brain", id, ".system_generated", "logs", "transcript.jsonl")
	info, err := os.Stat(p)
	if err != nil {
		return fileInfo{}, err
	}
	return fileInfo{path: p, mod: info.ModTime(), id: id}, nil
}

// sessionFiles returns every brain transcript under <root>/brain, newest
// first.
func sessionFiles(root string) ([]fileInfo, error) {
	dir := filepath.Join(root, "brain")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	convs, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []fileInfo
	for _, c := range convs {
		if !c.IsDir() {
			continue
		}
		fi, err := transcriptOf(root, c.Name())
		if err != nil {
			continue
		}
		files = append(files, fi)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	return files, nil
}

// --- history index ----------------------------------------------------------

// historyEntry is one line of <root>/history.jsonl: the prompt the user typed
// (display), where and when they typed it (workspace, epoch-millisecond
// timestamp), and which conversation it belongs to — absent on a
// conversation's opening prompt, which is logged before the id exists.
type historyEntry struct {
	Display        string `json:"display"`
	Workspace      string `json:"workspace"`
	Timestamp      int64  `json:"timestamp"`
	ConversationID string `json:"conversationId"`
}

// history indexes history.jsonl two ways: byID for lines that carry a
// conversation id (a conversation's second prompt onward), and starts for the
// id-less opening prompts that matchStart joins back to transcripts by
// content. Single-prompt conversations exist only in starts.
type history struct {
	byID   map[string]historyEntry
	starts []historyEntry
}

// loadHistory reads <root>/history.jsonl. A missing or unreadable file yields
// an empty index, which degrades listing metadata but never fails a read.
func loadHistory(root string) history {
	h := history{byID: map[string]historyEntry{}}
	f, err := os.Open(filepath.Join(root, "history.jsonl"))
	if err != nil {
		return h
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for dec.More() {
		var e historyEntry
		if dec.Decode(&e) != nil {
			break
		}
		if e.ConversationID == "" {
			h.starts = append(h.starts, e)
			continue
		}
		if first, ok := h.byID[e.ConversationID]; ok {
			e.Display = first.Display // keep the earliest prompt as the title
		}
		h.byID[e.ConversationID] = e
	}
	return h
}

// matchStart finds the id-less history line that opened this conversation:
// the line whose display text is the transcript's first user turn. The same
// prompt typed in different directories ("housekeeping") ties on text, so the
// closest timestamp to that turn wins.
func (h history) matchStart(entries []session.Entry) (historyEntry, bool) {
	var first session.Entry
	for _, e := range entries {
		if e.Kind == session.KindMessage && e.Role == session.RoleUser {
			first = e
			break
		}
	}
	if first.Text == "" {
		return historyEntry{}, false
	}
	var best historyEntry
	bestDiff := int64(-1)
	for _, s := range h.starts {
		if strings.TrimSpace(s.Display) != first.Text {
			continue
		}
		diff := s.Timestamp - first.Time.UnixMilli()
		if diff < 0 {
			diff = -diff
		}
		if bestDiff < 0 || diff < bestDiff {
			best, bestDiff = s, diff
		}
	}
	return best, bestDiff >= 0
}

// applyStart fills cwd/title from the matchStart join for a Source that
// history.jsonl does not locate by id. A no-op when cwd is already known.
func applyStart(src *session.Source, entries []session.Entry, h history) {
	if src.Metadata["cwd"] != "" {
		return
	}
	s, ok := h.matchStart(entries)
	if !ok {
		return
	}
	src.Metadata["cwd"] = s.Workspace
	if src.Metadata["title"] == "" {
		src.Metadata["title"] = s.Display
	}
}

func sourceOf(fi fileInfo, h history) session.Source {
	src := session.Source{
		Ref:       session.Ref{Provider: session.ProviderAgy, SessionID: fi.id},
		Path:      fi.path,
		UpdatedAt: fi.mod,
		Metadata:  map[string]string{},
	}
	if e, ok := h.byID[fi.id]; ok {
		if e.Workspace != "" {
			src.Metadata["cwd"] = e.Workspace
		}
		if e.Display != "" {
			src.Metadata["title"] = e.Display
		}
	}
	if src.Metadata["title"] == "" {
		if cwd := src.Metadata["cwd"]; cwd != "" {
			src.Metadata["title"] = filepath.Base(cwd)
		}
	}
	return src
}

// --- listing ----------------------------------------------------------------

// listSessions walks transcripts newest-first and collects up to limit
// summaries. When cwd is set, only conversations located in that directory by
// history.jsonl (by id, or by matchStart for single-prompt conversations) are
// included — which also excludes subagent conversations, since those never
// reach history.jsonl.
func listSessions(root, query, cwd string, limit int) ([]session.Summary, error) {
	files, err := sessionFiles(root)
	if err != nil {
		return nil, err
	}
	h := loadHistory(root)
	q := strings.ToLower(query)
	out := make([]session.Summary, 0, limit)
	for _, fi := range files {
		if len(out) >= limit {
			break
		}
		entries, _, err := readEntries(fi)
		if err != nil || len(entries) == 0 {
			continue
		}
		src := sourceOf(fi, h)
		applyStart(&src, entries, h)
		if cwd != "" && src.Metadata["cwd"] != cwd {
			continue
		}
		t := session.Thread{Source: src, Entries: entries}
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

type agyStep struct {
	Source    string `json:"source"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Content   string `json:"content"`
}

func readEntries(fi fileInfo) ([]session.Entry, []string, error) {
	f, err := os.Open(fi.path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	var entries []session.Entry
	var warnings []string
	dec := json.NewDecoder(f)
	for dec.More() {
		var step agyStep
		if dec.Decode(&step) != nil {
			warnings = append(warnings, "stopped reading at a malformed record")
			break
		}
		ts := parseTime(step.CreatedAt)
		switch step.Type {
		case "USER_INPUT":
			if text := userRequest(step.Content); text != "" {
				entries = append(entries, session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: text, Time: ts})
			}
		case "PLANNER_RESPONSE":
			if step.Source == "MODEL" && step.Content != "" {
				entries = append(entries, session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: step.Content, Time: ts})
			}
		case "CHECKPOINT":
			if step.Content != "" {
				entries = append(entries, session.Entry{Kind: session.KindCompact, Text: step.Content, Time: ts})
			}
		}
	}
	return entries, warnings, nil
}

// userRequest extracts what the user actually typed from a USER_INPUT step:
// the body of its <USER_REQUEST> element, without the ADDITIONAL_METADATA /
// USER_SETTINGS_CHANGE blocks the CLI appends. Content with no such element
// is returned whole, so a future format change degrades to noise rather than
// silence.
func userRequest(content string) string {
	_, rest, ok := strings.Cut(content, "<USER_REQUEST>")
	if !ok {
		return strings.TrimSpace(content)
	}
	body, _, ok := strings.Cut(rest, "</USER_REQUEST>")
	if !ok {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(body)
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
