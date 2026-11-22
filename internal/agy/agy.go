// Package agy implements session.Provider over Antigravity CLI history:
// step transcripts under
// <root>/brain/<conversation-id>/.system_generated/logs/{transcript_full,transcript}.jsonl
// (default ~/.gemini/antigravity-cli; verified against agy 1.5.14).
//
// Antigravity writes two JSONL logs per conversation: transcript.jsonl
// truncates long fields (<truncated N bytes>) and transcript_full.jsonl keeps
// them whole. Older sessions can miss a prefix or single step in one of the
// two files, and compaction replays can duplicate a step_index range or emit
// steps slightly out of index order. Reading merges both files by step_index
// (transcript_full.jsonl wins on collision, last occurrence wins within a
// file) and sorts ascending.
//
// Useful steps: USER_INPUT (the user turn, wrapped in <USER_REQUEST> tags),
// PLANNER_RESPONSE from MODEL (assistant prose, model name, and tool_calls as
// KindToolCall), CHECKPOINT (compaction marker), ERROR_MESSAGE (provider API
// or model-output stop as KindStop), and tool steps with status "ERROR"
// (KindFailure, paired in order with the preceding PLANNER_RESPONSE's
// tool_calls). Successful tool steps — including agy 1.5.14 shell commands
// logged as GENERIC with exit codes only in prose — and bookkeeping steps are
// skipped.
//
// Metadata comes from <root>/conversation_summaries.db (opened read-only via
// internal/sqlitedb for WAL visibility): native title, workspace_uris, and
// subagent hierarchy (parent_conversation_id, nesting_depth, agent_name).
// Subagents marked there are hidden from List and bare Resolve, while explicit
// Resolve(id) still returns them with their parent/role metadata.
// <root>/history.jsonl remains the fallback for older sessions with empty
// workspace_uris or an absent database: slash-command lines are ignored as
// titles, and id-less opening prompts join back by first-turn text (closest
// timestamp on ties); see history.matchStart.
package agy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
	"github.com/wilbeibi/catchup/internal/sqlitedb"
)

// Provider reads Antigravity CLI brain transcripts.
type Provider struct{}

// New returns an Antigravity provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	// The transcript path is a pure function of the conversation id, so an
	// explicit id needs no enumeration.
	if id != "" {
		fi, err := transcriptOf(roots.Agy, id)
		if err != nil {
			return session.Source{}, fmt.Errorf("agy: no session with id %q", id)
		}
		return resolveSource(fi, loadSummaries(roots.Agy), loadHistory(roots.Agy)), nil
	}
	files, err := sessionFiles(roots.Agy)
	if err != nil {
		return session.Source{}, err
	}
	db := loadSummaries(roots.Agy)
	h := loadHistory(roots.Agy)
	for _, fi := range files {
		if !isSubagent(sourceOf(fi, db, h)) {
			return resolveSource(fi, db, h), nil
		}
	}
	return session.Source{}, fmt.Errorf("agy: no sessions found under %s", roots.Agy)
}

// resolveSource builds a Source for one transcript, paying one extra
// transcript read for the matchStart join when neither conversation_summaries.db
// nor history.jsonl locates it by id.
func resolveSource(fi fileInfo, db map[string]dbSummary, h history) session.Source {
	src := sourceOf(fi, db, h)
	if src.Metadata["cwd"] == "" {
		if entries, _, _, err := readEntries(fi); err == nil {
			applyStart(&src, entries, h)
		}
	}
	finalizeTitle(&src)
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
	fi := fileInfoFromPath(src.Path, info.ModTime(), src.Ref.SessionID)
	entries, model, warnings, err := readEntries(fi)
	if err != nil {
		return session.Thread{}, err
	}
	if model != "" {
		if src.Metadata == nil {
			src.Metadata = map[string]string{}
		}
		src.Metadata["model"] = model
	}
	return session.Thread{Source: src, Entries: entries, Warnings: warnings}, nil
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	return listSessions(roots.Agy, opts)
}

// --- file enumeration -------------------------------------------------------

const (
	compactLogName = "transcript.jsonl"
	fullLogName    = "transcript_full.jsonl"
)

type fileInfo struct {
	path    string // canonical path for Source.Path (full when present, else compact)
	compact string // transcript.jsonl, when present
	full    string // transcript_full.jsonl, when present
	mod     time.Time
	id      string // conversation id, the brain directory's name
}

// transcriptOf stats the brain transcript(s) belonging to one conversation id,
// preferring transcript_full.jsonl as the primary path while retaining
// transcript.jsonl so missing prefixes in either file are recovered.
func transcriptOf(root, id string) (fileInfo, error) {
	dir := filepath.Join(root, "brain", id, ".system_generated", "logs")
	full := filepath.Join(dir, fullLogName)
	compact := filepath.Join(dir, compactLogName)
	fiFull, errFull := os.Stat(full)
	fiCompact, errCompact := os.Stat(compact)
	switch {
	case errFull == nil && errCompact == nil:
		mod := fiFull.ModTime()
		if fiCompact.ModTime().After(mod) {
			mod = fiCompact.ModTime()
		}
		return fileInfo{path: full, compact: compact, full: full, mod: mod, id: id}, nil
	case errFull == nil:
		return fileInfo{path: full, full: full, mod: fiFull.ModTime(), id: id}, nil
	case errCompact == nil:
		return fileInfo{path: compact, compact: compact, mod: fiCompact.ModTime(), id: id}, nil
	default:
		return fileInfo{}, errCompact
	}
}

// fileInfoFromPath rebuilds a fileInfo from a located Source.Path, attaching
// its companion transcript file if one sits beside it.
func fileInfoFromPath(path string, mod time.Time, id string) fileInfo {
	fi := fileInfo{path: path, mod: mod, id: id}
	dir := filepath.Dir(path)
	switch filepath.Base(path) {
	case fullLogName:
		fi.full = path
		if c := filepath.Join(dir, compactLogName); fileExists(c) {
			fi.compact = c
		}
	case compactLogName:
		fi.compact = path
		if f := filepath.Join(dir, fullLogName); fileExists(f) {
			fi.full = f
		}
	default:
		fi.full = path
	}
	return fi
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

// --- SQLite summary index ---------------------------------------------------

type dbSummary struct {
	title     string
	cwd       string
	parent    string
	depth     int
	agentName string
}

// loadSummaries reads conversation metadata from <root>/conversation_summaries.db.
// Missing or unreadable databases yield nil so reads degrade cleanly to
// history.jsonl without failing.
func loadSummaries(root string) map[string]dbSummary {
	path := filepath.Join(root, "conversation_summaries.db")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	db, err := sqlitedb.Open(path)
	if err != nil {
		return nil
	}
	defer db.Close()

	rows, err := db.Query(`SELECT conversation_id, COALESCE(title, ''), COALESCE(workspace_uris, ''), COALESCE(parent_conversation_id, ''), COALESCE(nesting_depth, 0), COALESCE(agent_name, '') FROM conversation_summaries`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	out := map[string]dbSummary{}
	for rows.Next() {
		var id, title, wsURIs, parent, agent string
		var depth int
		if rows.Scan(&id, &title, &wsURIs, &parent, &depth, &agent) != nil || id == "" {
			continue
		}
		out[id] = dbSummary{
			title:     strings.TrimSpace(title),
			cwd:       firstWorkspace(wsURIs),
			parent:    parent,
			depth:     depth,
			agentName: agent,
		}
	}
	return out
}

func firstWorkspace(raw string) string {
	var uris []string
	if json.Unmarshal([]byte(raw), &uris) != nil {
		return ""
	}
	for _, uri := range uris {
		if p := fileURIPath(uri); p != "" {
			return p
		}
	}
	return ""
}

func fileURIPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme == "" {
		if raw == "" {
			return ""
		}
		return filepath.Clean(raw)
	}
	if u.Scheme != "file" {
		return ""
	}
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	if p == "" {
		return ""
	}
	return filepath.FromSlash(p)
}

// --- history index ----------------------------------------------------------

// historyEntry is one line of <root>/history.jsonl: the prompt the user typed
// (display), where and when they typed it (workspace, epoch-millisecond
// timestamp), and which conversation it belongs to (absent on opening
// prompts; see package doc).
type historyEntry struct {
	Display        string `json:"display"`
	Workspace      string `json:"workspace"`
	Timestamp      int64  `json:"timestamp"`
	ConversationID string `json:"conversationId"`
	Type           string `json:"type"`
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
		if e.Type == "slash_command" {
			e.Display = ""
			if e.ConversationID == "" {
				continue
			}
		}
		if e.ConversationID == "" {
			h.starts = append(h.starts, e)
			continue
		}
		if prev, ok := h.byID[e.ConversationID]; ok {
			if prev.Display != "" {
				e.Display = prev.Display // keep the earliest prompt as the title
			}
			if e.Workspace == "" {
				e.Workspace = prev.Workspace
			}
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

// applyStart fills cwd/title from the matchStart join when they are still
// empty after checking conversation_summaries.db and history.jsonl by id.
func applyStart(src *session.Source, entries []session.Entry, h history) {
	if src.Metadata["cwd"] != "" && src.Metadata["title"] != "" {
		return
	}
	s, ok := h.matchStart(entries)
	if !ok {
		return
	}
	if src.Metadata["cwd"] == "" && s.Workspace != "" {
		src.Metadata["cwd"] = s.Workspace
	}
	if src.Metadata["title"] == "" && s.Display != "" {
		src.Metadata["title"] = s.Display
	}
}

func sourceOf(fi fileInfo, db map[string]dbSummary, h history) session.Source {
	src := session.Source{
		Ref:       session.Ref{Provider: session.ProviderAgy, SessionID: fi.id},
		Path:      fi.path,
		UpdatedAt: fi.mod,
		Metadata:  map[string]string{},
	}
	if s, ok := db[fi.id]; ok {
		if s.cwd != "" {
			src.Metadata["cwd"] = s.cwd
		}
		if s.title != "" {
			src.Metadata["title"] = s.title
			src.Metadata["native_title"] = s.title
		}
		if s.parent != "" || s.depth > 0 {
			src.Metadata["relationship"] = "child"
			if s.parent != "" {
				src.Metadata["parent"] = s.parent
			}
		}
		if s.agentName != "" {
			src.Metadata["agent_role"] = s.agentName
		}
	}
	if e, ok := h.byID[fi.id]; ok {
		if src.Metadata["cwd"] == "" && e.Workspace != "" {
			src.Metadata["cwd"] = e.Workspace
		}
		if src.Metadata["title"] == "" && e.Display != "" {
			src.Metadata["title"] = e.Display
		}
	}
	return src
}

func finalizeTitle(src *session.Source) {
	if src.Metadata["title"] == "" {
		if cwd := src.Metadata["cwd"]; cwd != "" {
			src.Metadata["title"] = filepath.Base(cwd)
		}
	}
}

func isSubagent(src session.Source) bool {
	return src.Metadata["relationship"] == "child"
}

// --- listing ----------------------------------------------------------------

// listSessions walks transcripts newest-first and collects up to limit
// summaries. Subagents marked in conversation_summaries.db are hidden.
func listSessions(root string, opts session.ListOptions) ([]session.Summary, error) {
	files, err := sessionFiles(root)
	if err != nil {
		return nil, err
	}
	db := loadSummaries(root)
	h := loadHistory(root)
	limit := opts.EffectiveLimit()
	out := make([]session.Summary, 0, limit)
	for _, fi := range files {
		if len(out) >= limit {
			break
		}
		src := sourceOf(fi, db, h)
		if isSubagent(src) {
			continue
		}
		// conversation_summaries.db and history.jsonl locate most conversations
		// by id; reject non-matching directories before parsing the transcript.
		if src.Metadata["cwd"] != "" && !opts.MatchesCwd(src.Metadata["cwd"]) {
			continue
		}
		if opts.Query != "" && !(session.Thread{Source: src}).MatchesTitle(opts.Query) && !mayMatchFiles(fi, opts) {
			continue
		}
		entries, model, _, err := readEntries(fi)
		if err != nil {
			continue
		}
		if model != "" {
			src.Metadata["model"] = model
		}
		applyStart(&src, entries, h)
		finalizeTitle(&src)
		t := session.Thread{Source: src, Entries: entries}
		if !opts.Listable(&t) || !opts.Matches(t) {
			continue
		}
		out = append(out, opts.Summarize(t))
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, nil
}

func mayMatchFiles(fi fileInfo, opts session.ListOptions) bool {
	for _, p := range []string{fi.full, fi.compact} {
		if p == "" {
			continue
		}
		if raw, err := os.ReadFile(p); err == nil && opts.MayMatchQuery(raw) {
			return true
		}
	}
	return false
}

// --- parsing ----------------------------------------------------------------

type agyToolCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type agyStep struct {
	StepIndex *int          `json:"step_index"`
	Source    string        `json:"source"`
	Type      string        `json:"type"`
	Status    string        `json:"status"`
	CreatedAt string        `json:"created_at"`
	Content   string        `json:"content"`
	Model     string        `json:"model"`
	Error     string        `json:"error"`
	ErrorCode int           `json:"error_code"`
	ToolCalls []agyToolCall `json:"tool_calls"`
}

func readEntries(fi fileInfo) ([]session.Entry, string, []string, error) {
	steps, warnings, err := loadMergedSteps(fi)
	if err != nil {
		return nil, "", nil, err
	}

	var entries []session.Entry
	var unknown session.UnknownTypes
	var model string
	var pending []agyToolCall

	for _, step := range steps {
		ts := session.ParseTime(step.CreatedAt)
		switch step.Type {
		case "USER_INPUT":
			pending = nil
			if text := userRequest(step.Content); text != "" {
				entries = append(entries, session.Entry{Kind: session.KindMessage, Role: session.RoleUser, Text: text, Time: ts})
			}
		case "PLANNER_RESPONSE":
			if step.Source == "MODEL" {
				if step.Model != "" {
					model = step.Model
				}
				if step.Content != "" {
					entries = append(entries, session.Entry{Kind: session.KindMessage, Role: session.RoleAssistant, Text: step.Content, Time: ts})
				}
				pending = nil
				for _, tc := range step.ToolCalls {
					if tc.Name != "" {
						entries = append(entries, session.ToolCall(tc.Name, tc.Args, ts))
					}
					pending = append(pending, tc)
				}
			}
		case "CHECKPOINT":
			pending = nil
			if step.Content != "" {
				entries = append(entries, session.Entry{Kind: session.KindCompact, Text: step.Content, Time: ts})
			}
		case "ERROR_MESSAGE":
			pending = nil
			if text := stepErrorText(step); text != "" {
				reason := "error"
				if step.ErrorCode != 0 {
					reason = strconv.Itoa(step.ErrorCode)
				}
				entries = append(entries, session.Entry{Kind: session.KindStop, Reason: reason, Text: text, Time: ts})
			}
		case "RUN_COMMAND", "VIEW_FILE", "LIST_DIRECTORY", "GREP_SEARCH", "SEARCH_WEB",
			"CODE_ACTION", "GENERATE_IMAGE", "INVOKE_SUBAGENT", "ASK_QUESTION", "GENERIC":
			var call agyToolCall
			if len(pending) > 0 {
				call = pending[0]
				pending = pending[1:]
			}
			if step.Status == "ERROR" {
				tool := call.Name
				if tool == "" {
					tool = strings.ToLower(step.Type)
				}
				entries = append(entries, session.Failure(tool, call.Args, stepErrorText(step), ts))
			}
		case "EPHEMERAL_MESSAGE", "SYSTEM_MESSAGE", "CONVERSATION_HISTORY":
			// Status lines, injected context, and the CLI's own replay.
		default:
			unknown.Add(step.Type)
		}
	}
	return entries, model, unknown.AppendTo(warnings), nil
}

func stepErrorText(step agyStep) string {
	if text := strings.TrimSpace(step.Error); text != "" {
		return text
	}
	return strings.TrimSpace(step.Content)
}

// loadMergedSteps reads transcript.jsonl first and overlays
// transcript_full.jsonl on top, keying by step_index so untruncated full steps
// override truncated compact ones, missing prefixes in either file are
// recovered, duplicate replay runs collapse, and out-of-order steps sort into
// chronological step order.
func loadMergedSteps(fi fileInfo) ([]agyStep, []string, error) {
	byIndex := map[int]agyStep{}
	var unindexed []agyStep
	var warnings []string

	for _, p := range []string{fi.compact, fi.full} {
		if p == "" {
			continue
		}
		if err := decodeStepsFile(p, byIndex, &unindexed, &warnings); err != nil {
			if p == fi.path {
				return nil, nil, err
			}
		}
	}

	keys := make([]int, 0, len(byIndex))
	for k := range byIndex {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	steps := make([]agyStep, 0, len(keys)+len(unindexed))
	for _, k := range keys {
		steps = append(steps, byIndex[k])
	}
	steps = append(steps, unindexed...)
	return steps, warnings, nil
}

func decodeStepsFile(path string, byIndex map[int]agyStep, unindexed *[]agyStep, warnings *[]string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	for dec.More() {
		var step agyStep
		if err := dec.Decode(&step); err != nil {
			*warnings = append(*warnings, session.ReadStopWarning(err))
			break
		}
		if step.StepIndex != nil {
			byIndex[*step.StepIndex] = step
		} else {
			*unindexed = append(*unindexed, step)
		}
	}
	return nil
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
