// Package claude implements session.Provider over Claude Code history: project
// JSONL under $CLAUDE_CONFIG_DIR/projects/**/<uuid>.jsonl (default
// ~/.claude).
//
// Useful records: top-level user and assistant entries; message.content as a
// string or as text blocks; compact summaries from the known compact/summary
// system records; ai-title, cwd, gitBranch, sessionId, and timestamp for
// metadata and listing.
//
// Ignored by default: tool_use, tool_result, thinking, queue/mode/permission
// bookkeeping, file-history snapshots, last-prompt, subagent (isSidechain) and
// injected (isMeta) entries, subagent files under */subagents/*, and
// .claude/transcripts/ses_*.jsonl (v1).
package claude

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

// Provider reads Claude Code project transcripts.
type Provider struct{}

// New returns a Claude provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	files, err := sessionFiles(roots.Claude)
	if err != nil {
		return session.Source{}, err
	}
	if len(files) == 0 {
		return session.Source{}, fmt.Errorf("claude: no sessions found under %s", roots.Claude)
	}
	if id == "" {
		return readMeta(files[0])
	}
	for _, fi := range files {
		// The session id is the file's base name; confirm against the records.
		if strings.TrimSuffix(filepath.Base(fi.path), ".jsonl") != id {
			continue
		}
		return readMeta(fi)
	}
	for _, fi := range files {
		src, err := readMeta(fi)
		if err == nil && src.Ref.SessionID == id {
			return src, nil
		}
	}
	return session.Source{}, fmt.Errorf("claude: no session with id %q", id)
}

func (p *Provider) ResolveRank(ctx context.Context, roots session.Roots, opts session.ListOptions, rank int) (session.Source, error) {
	if rank < 1 {
		return session.Source{}, fmt.Errorf("claude: rank must be >= 1")
	}
	sums, err := listSessions(roots.Claude, opts.Query, rank)
	if err != nil {
		return session.Source{}, err
	}
	if rank > len(sums) {
		return session.Source{}, fmt.Errorf("claude: rank %d out of range (%d matching sessions)", rank, len(sums))
	}
	return p.Resolve(ctx, roots, sums[rank-1].Ref.SessionID)
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Path == "" {
		return session.Thread{}, errors.New("claude: source has no path")
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
	return listSessions(roots.Claude, opts.Query, limit)
}

// --- file enumeration -------------------------------------------------------

type fileInfo struct {
	path string
	mod  time.Time
}

// sessionFiles returns every project transcript under <root>/projects, newest
// first. Subagent sidechain files are skipped.
func sessionFiles(root string) ([]fileInfo, error) {
	dir := filepath.Join(root, "projects")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var files []fileInfo
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		if strings.Contains(p, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
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

// --- listing ----------------------------------------------------------------

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
			continue // skip empty/unreadable transcripts
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

type claudeLine struct {
	Type             string         `json:"type"`
	SessionID        string         `json:"sessionId"`
	Cwd              string         `json:"cwd"`
	GitBranch        string         `json:"gitBranch"`
	Timestamp        string         `json:"timestamp"`
	IsMeta           bool           `json:"isMeta"`
	IsSidechain      bool           `json:"isSidechain"`
	IsCompactSummary bool           `json:"isCompactSummary"`
	AiTitle          string         `json:"aiTitle"`
	Message          *claudeMessage `json:"message"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// readMeta scans a transcript for metadata only (session id, cwd, branch,
// ai-title) without building the timeline. It reads the whole file, but Resolve
// only ever does this for a single session, so the cost is bounded.
func readMeta(fi fileInfo) (session.Source, error) {
	f, err := os.Open(fi.path)
	if err != nil {
		return session.Source{}, err
	}
	defer f.Close()

	src := newSource(fi)
	dec := json.NewDecoder(f)
	for dec.More() {
		var line claudeLine
		if dec.Decode(&line) != nil {
			break
		}
		applyMeta(&src, line)
	}
	finalizeMeta(&src)
	return src, nil
}

func readThread(fi fileInfo) (session.Thread, error) {
	f, err := os.Open(fi.path)
	if err != nil {
		return session.Thread{}, err
	}
	defer f.Close()

	src := newSource(fi)
	var entries []session.Entry
	var warnings []string

	dec := json.NewDecoder(f)
	for dec.More() {
		var line claudeLine
		if dec.Decode(&line) != nil {
			warnings = append(warnings, "skipped a malformed record")
			break
		}
		applyMeta(&src, line)

		if line.Type != "user" && line.Type != "assistant" {
			continue
		}
		if line.IsSidechain || line.Message == nil {
			continue // subagent turns live in their own thread
		}
		text := extractText(line.Message.Content)
		if text == "" {
			continue
		}
		ts := parseTime(line.Timestamp)

		if line.IsCompactSummary {
			entries = append(entries, session.Entry{Kind: session.KindCompact, Text: text, Time: ts})
			continue
		}
		if line.IsMeta {
			continue // injected environment/context, not a real turn
		}
		role := normalizeRole(line.Message.Role)
		if role == "" {
			continue
		}
		entries = append(entries, session.Entry{Kind: session.KindMessage, Role: role, Text: text, Time: ts})
	}

	finalizeMeta(&src)
	return session.Thread{Source: src, Entries: entries, Warnings: warnings}, nil
}

func newSource(fi fileInfo) session.Source {
	return session.Source{
		Ref:       session.Ref{Provider: session.ProviderClaude, SessionID: strings.TrimSuffix(filepath.Base(fi.path), ".jsonl")},
		Path:      fi.path,
		UpdatedAt: fi.mod,
		Metadata:  map[string]string{},
	}
}

func applyMeta(src *session.Source, line claudeLine) {
	if line.SessionID != "" {
		src.Ref.SessionID = line.SessionID
	}
	if line.Cwd != "" {
		src.Metadata["cwd"] = line.Cwd
	}
	if line.GitBranch != "" {
		src.Metadata["branch"] = line.GitBranch
	}
	if line.AiTitle != "" {
		src.Metadata["title"] = line.AiTitle
	}
}

// finalizeMeta supplies a title fallback when no ai-title record was present.
func finalizeMeta(src *session.Source) {
	if src.Metadata["title"] == "" {
		if cwd := src.Metadata["cwd"]; cwd != "" {
			src.Metadata["title"] = filepath.Base(cwd)
		}
	}
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// content may be a plain string (user turns) ...
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	// ... or an array of typed blocks (assistant turns; keep only text).
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func normalizeRole(role string) string {
	switch role {
	case session.RoleUser, session.RoleAssistant:
		return role
	default:
		return ""
	}
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
