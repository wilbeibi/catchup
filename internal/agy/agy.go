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
// top-level ones. The CLI's <root>/history.jsonl, however, records only
// user-initiated conversations ({display, workspace, conversationId}), so it
// doubles as the directory index: cwd filtering matches its workspace field,
// which also keeps subagents out of listings. Conversations older than the
// conversationId field predates are reachable by --id only.
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

const defaultLimit = 20

// Provider reads Antigravity CLI brain transcripts.
type Provider struct{}

// New returns an Antigravity provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	files, err := sessionFiles(roots.Agy)
	if err != nil {
		return session.Source{}, err
	}
	if len(files) == 0 {
		return session.Source{}, fmt.Errorf("agy: no sessions found under %s", roots.Agy)
	}
	if id == "" {
		return sourceOf(files[0], loadHistory(roots.Agy)), nil
	}
	for _, fi := range files {
		if fi.id == id {
			return sourceOf(fi, loadHistory(roots.Agy)), nil
		}
	}
	return session.Source{}, fmt.Errorf("agy: no session with id %q", id)
}

func (p *Provider) ResolveRank(ctx context.Context, roots session.Roots, opts session.ListOptions, rank int) (session.Source, error) {
	if rank < 1 {
		return session.Source{}, fmt.Errorf("agy: rank must be >= 1")
	}
	sums, err := listSessions(roots.Agy, opts.Query, opts.Cwd, rank)
	if err != nil {
		return session.Source{}, err
	}
	if rank > len(sums) {
		return session.Source{}, fmt.Errorf("agy: rank %d out of range (%d matching sessions)", rank, len(sums))
	}
	return p.Resolve(ctx, roots, sums[rank-1].Ref.SessionID)
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
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	return listSessions(roots.Agy, opts.Query, opts.Cwd, limit)
}

// --- file enumeration -------------------------------------------------------

type fileInfo struct {
	path string
	mod  time.Time
	id   string // conversation id, the brain directory's name
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
		p := filepath.Join(dir, c.Name(), ".system_generated", "logs", "transcript.jsonl")
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		files = append(files, fileInfo{path: p, mod: info.ModTime(), id: c.Name()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	return files, nil
}

// --- history index ----------------------------------------------------------

// historyEntry is one line of <root>/history.jsonl: the prompt the user typed
// (display), where they typed it (workspace), and which conversation it
// started or continued. Lines from older CLIs lack conversationId.
type historyEntry struct {
	Display        string `json:"display"`
	Workspace      string `json:"workspace"`
	ConversationID string `json:"conversationId"`
}

// loadHistory indexes history.jsonl by conversation id. The first entry for a
// conversation names it (Display); the workspace of any entry locates it.
// A missing or unreadable file yields an empty index, which degrades listing
// metadata but never fails a read.
func loadHistory(root string) map[string]historyEntry {
	index := map[string]historyEntry{}
	f, err := os.Open(filepath.Join(root, "history.jsonl"))
	if err != nil {
		return index
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for dec.More() {
		var e historyEntry
		if dec.Decode(&e) != nil {
			break
		}
		if e.ConversationID == "" {
			continue
		}
		if first, ok := index[e.ConversationID]; ok {
			e.Display = first.Display // keep the opening prompt as the title
		}
		index[e.ConversationID] = e
	}
	return index
}

func sourceOf(fi fileInfo, index map[string]historyEntry) session.Source {
	src := session.Source{
		Ref:       session.Ref{Provider: session.ProviderAgy, SessionID: fi.id},
		Path:      fi.path,
		UpdatedAt: fi.mod,
		Metadata:  map[string]string{},
	}
	if e, ok := index[fi.id]; ok {
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
// summaries. When cwd is set, only conversations whose history.jsonl
// workspace matches exactly are included — which also excludes subagent
// conversations, since those never reach history.jsonl.
func listSessions(root, query, cwd string, limit int) ([]session.Summary, error) {
	files, err := sessionFiles(root)
	if err != nil {
		return nil, err
	}
	index := loadHistory(root)
	q := strings.ToLower(query)
	out := make([]session.Summary, 0, limit)
	for _, fi := range files {
		if len(out) >= limit {
			break
		}
		if cwd != "" && index[fi.id].Workspace != cwd {
			continue
		}
		src := sourceOf(fi, index)
		entries, _, err := readEntries(fi)
		if err != nil || len(entries) == 0 {
			continue
		}
		t := session.Thread{Source: src, Entries: entries}
		if q != "" && !strings.Contains(strings.ToLower(visibleText(t)), q) {
			continue
		}
		out = append(out, session.Summary{
			Ref:       src.Ref,
			UpdatedAt: src.UpdatedAt,
			Title:     src.Metadata["title"],
			Cwd:       src.Metadata["cwd"],
			Preview:   preview(t),
		})
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, nil
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
			warnings = append(warnings, "skipped a malformed record")
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
