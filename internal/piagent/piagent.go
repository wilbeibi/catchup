// Package piagent implements session.Provider over Pi coding agent history:
// JSONL files under $PI_CODING_AGENT_DIR/sessions (default ~/.pi/agent).
//
// Format reference, source of truth for the record shapes and the parentId tree:
// https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/session-format.md
//
// Useful records: session.{id,cwd,timestamp,parentSession} for metadata;
// model_change.{provider,modelId} and assistant message provider/model on the
// current parent chain for model metadata; session_info.name for title;
// message.role user/assistant with text content for the timeline;
// compaction.summary as a compaction marker.
//
// Ignored by default: toolCall content blocks, toolResult messages, thinking
// blocks, labels, custom messages, token usage, and branch bookkeeping.
package piagent

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

// Provider reads Pi coding agent session files.
type Provider struct{}

// New returns a Pi provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	files, err := sessionFiles(roots.PiAgent)
	if err != nil {
		return session.Source{}, err
	}
	if len(files) == 0 {
		return session.Source{}, fmt.Errorf("pi-agent: no sessions found under %s", roots.PiAgent)
	}
	if id == "" {
		return readMeta(files[0])
	}
	if src, ok := findByID(files, id, true); ok {
		return src, nil
	}
	if src, ok := findByID(files, id, false); ok {
		return src, nil
	}
	return session.Source{}, fmt.Errorf("pi-agent: no session with id %q", id)
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Path == "" {
		return session.Thread{}, errors.New("pi-agent: source has no path")
	}
	info, err := os.Stat(src.Path)
	if err != nil {
		return session.Thread{}, err
	}
	return readThread(fileInfo{path: src.Path, mod: info.ModTime()})
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	return listSessions(roots.PiAgent, opts.Query, opts.Cwd, opts.EffectiveLimit())
}

// --- file enumeration -------------------------------------------------------

type fileInfo struct {
	path string
	mod  time.Time
}

// sessionFiles returns every Pi JSONL session under <root>/sessions, newest
// first. Pi stores sessions one directory per cwd, so this walks recursively.
func sessionFiles(root string) ([]fileInfo, error) {
	dir := filepath.Join(root, "sessions")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var files []fileInfo
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
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
		if cwd != "" && t.Source.Metadata["cwd"] != cwd {
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

type piLine struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	ParentID  string    `json:"parentId"`
	Timestamp string    `json:"timestamp"`
	Cwd       string    `json:"cwd"`
	Parent    string    `json:"parentSession"`
	Name      string    `json:"name"`
	Provider  string    `json:"provider"`
	ModelID   string    `json:"modelId"`
	Summary   string    `json:"summary"`
	Message   piMessage `json:"message"`
}

type piMessage struct {
	Role      string          `json:"role"`
	Timestamp int64           `json:"timestamp"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Content   json.RawMessage `json:"content"`
}

type piBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// readMeta delegates instead of doing a cheap metadata-only scan like the
// codex/claude providers: pi's session id and current model both sit behind
// currentPath, so a "lighter" read would parse the whole file anyway.
func readMeta(fi fileInfo) (session.Source, error) {
	t, err := readThread(fi)
	return t.Source, err
}

func readThread(fi fileInfo) (session.Thread, error) {
	f, err := os.Open(fi.path)
	if err != nil {
		return session.Thread{}, err
	}
	defer f.Close()

	src := newSource(fi)
	var warnings []string
	var lines []piLine

	dec := json.NewDecoder(f)
	for dec.More() {
		var line piLine
		if dec.Decode(&line) != nil {
			warnings = append(warnings, "stopped reading at a malformed record")
			break
		}
		lines = append(lines, line)
	}

	path := currentPath(lines)
	applyFileMeta(&src, lines)
	applyPathModelMeta(&src, path)
	finalizeMeta(&src)
	return session.Thread{Source: src, Entries: pathEntries(path), Warnings: warnings}, nil
}

func newSource(fi fileInfo) session.Source {
	return session.Source{
		Ref:       session.Ref{Provider: session.ProviderPiAgent},
		Path:      fi.path,
		UpdatedAt: fi.mod,
		Metadata:  map[string]string{},
	}
}

// applyFileMeta scans every line, not just the active branch, because the
// session header and session_info sit off the parentId path. A title set on an
// abandoned branch can win as a result, which is fine — titles aren't per-branch.
func applyFileMeta(src *session.Source, lines []piLine) {
	for _, line := range lines {
		switch line.Type {
		case "session":
			if line.ID != "" {
				src.Ref.SessionID = line.ID
			}
			if line.Cwd != "" {
				src.Metadata["cwd"] = line.Cwd
			}
			if line.Parent != "" {
				src.Metadata["parent"] = line.Parent
			}
		case "session_info":
			src.Metadata["title"] = strings.TrimSpace(line.Name)
		}
	}
}

// applyPathModelMeta keeps the last writer on the branch, so a bare
// message.model ("claude-sonnet-4") deliberately overrides an earlier
// model_change.modelId ("anthropic/claude-sonnet-4"): the assistant message
// records what actually answered.
func applyPathModelMeta(src *session.Source, path []piLine) {
	for _, line := range path {
		switch line.Type {
		case "model_change":
			if line.ModelID != "" {
				src.Metadata["model"] = line.ModelID
			}
			if line.Provider != "" {
				src.Metadata["model_provider"] = line.Provider
			}
		case "message":
			if line.Message.Role == session.RoleAssistant {
				if line.Message.Model != "" {
					src.Metadata["model"] = line.Message.Model
				}
				if line.Message.Provider != "" {
					src.Metadata["model_provider"] = line.Message.Provider
				}
			}
		}
	}
}

// currentPath walks the active branch leaf→root. There is deliberately no
// fallback for an entry missing parentId: pi sets it on every non-root entry,
// and migrates older linkless sessions before we ever read one, so a broken
// chain means a corrupt file rather than a shape worth limping along with. seen
// guards against a cycle in such a file.
func currentPath(lines []piLine) []piLine {
	byID := make(map[string]piLine, len(lines))
	var leaf string
	for _, line := range lines {
		if line.Type == "session" || line.ID == "" {
			continue
		}
		byID[line.ID] = line
		leaf = line.ID
	}
	if leaf == "" {
		return nil
	}
	var path []piLine
	seen := map[string]bool{}
	for id := leaf; id != ""; {
		line, ok := byID[id]
		if !ok || seen[id] {
			break
		}
		path = append(path, line)
		seen[id] = true
		id = line.ParentID
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

func pathEntries(path []piLine) []session.Entry {
	var entries []session.Entry
	for _, line := range path {
		switch line.Type {
		case "message":
			if e, ok := messageEntry(line); ok {
				entries = append(entries, e)
			}
		case "compaction":
			entries = append(entries, compactEntry(line))
		}
	}
	return entries
}

func messageEntry(line piLine) (session.Entry, bool) {
	if line.Type != "message" {
		return session.Entry{}, false
	}
	role := normalizeRole(line.Message.Role)
	if role == "" {
		return session.Entry{}, false
	}
	text := extractText(line.Message.Content)
	if text == "" {
		return session.Entry{}, false
	}
	ts := parseTime(line.Timestamp)
	if line.Message.Timestamp > 0 {
		ts = time.UnixMilli(line.Message.Timestamp)
	}
	return session.Entry{Kind: session.KindMessage, Role: role, Text: text, Time: ts}, true
}

func compactEntry(line piLine) session.Entry {
	return session.Entry{Kind: session.KindCompact, Text: line.Summary, Time: parseTime(line.Timestamp)}
}

func finalizeMeta(src *session.Source) {
	if src.Metadata["title"] == "" {
		if cwd := src.Metadata["cwd"]; cwd != "" {
			src.Metadata["title"] = filepath.Base(cwd)
		}
	}
}

func normalizeRole(role string) string {
	switch role {
	case session.RoleUser, session.RoleAssistant:
		return role
	default:
		return ""
	}
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []piBlock
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
