// Package deepseek implements session.Provider over DeepSeek Harness (dsh)
// history: per-session JSONL logs under $DSH_HOME/sessions (default
// ~/.dsh/sessions).
//
// Format reference, derived from the dsh session docs and a live install
// (github.com/deepseek-ai/deepseek-harness, docs/subsystems/session.md):
//
//	sessions/<munged-cwd>/session-<uuid>/session.jsonl.zstd holds one session;
//	the munged-cwd directory name replaces each "/" of the session's working
//	directory with "-" (the same shape Codex rollouts use). The physical
//	encoding is zstd-compressed JSONL by default; installs that configure
//	compression off write plain session.jsonl instead. Both are read here.
//
// The first line is a session header {"type":"session","version","id","cwd",
// "createdAt","agentPreset"}; every later line is one append-only event
// {type, seq, time, data} with epoch-ms times and contiguous seq. Packed
// chunk runs are stored as single rows (text-chunks/reasoning-chunks with
// seq0 instead of seq); they carry no timeline content and are skipped along
// with every other non-message event. A crashed writer can leave a torn final
// line; reading stops there and keeps the parsed prefix.
//
// Visible on the timeline: user/message events whose data.source.kind is
// "user" (plugin- and skill-catalog-sourced user rows are injected runtime
// context — sandbox snapshots, skill catalogs, tool modes — not conversation,
// and a live session is dominated by them) and assistant/message text blocks
// (reasoning blocks are skipped). Compaction seam events (type prefixed
// "compaction/") become compaction markers. Metadata: the header's id and
// cwd; session/title events, last writer wins; request/header's
// data.header.config {provider,model} plus each assistant message's
// data.message.source {provider,model}.
//
// Unverified: no local install had run compaction, so the compaction/* shapes
// and the replacement user/message rows the compaction plugins append (a
// non-"append" surfaceOp) come from the docs alone; replacements are skipped
// rather than guessed at, leaving the pre-compaction history visible.
package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/wilbeibi/catchup/internal/session"
)

// Provider reads dsh session logs. It is stateless; every call re-reads the
// log files, so a concurrently writing dsh is never blocked.
type Provider struct{}

// New returns a DeepSeek Harness provider.
func New() *Provider { return &Provider{} }

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	files, err := sessionFiles(roots.DeepSeek)
	if err != nil {
		return session.Source{}, err
	}
	if len(files) == 0 {
		return session.Source{}, fmt.Errorf("deepseek: no sessions found under %s", roots.DeepSeek)
	}
	if id != "" {
		for _, fi := range files {
			if fi.id == id {
				return readMeta(fi)
			}
		}
		return session.Source{}, fmt.Errorf("deepseek: no session with id %q", id)
	}
	return readMeta(files[0])
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if src.Path == "" {
		return session.Thread{}, errors.New("deepseek: source has no path")
	}
	info, err := os.Stat(src.Path)
	if err != nil {
		return session.Thread{}, err
	}
	return readThread(fileInfo{path: src.Path, mod: info.ModTime(), id: src.Ref.SessionID})
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	files, err := sessionFiles(roots.DeepSeek)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(opts.Query)
	limit := opts.EffectiveLimit()
	out := make([]session.Summary, 0, limit)
	for _, fi := range files {
		if len(out) >= limit {
			break
		}
		t, err := readThread(fi)
		if err != nil || len(t.Entries) == 0 {
			continue
		}
		if opts.Cwd != "" && t.Source.Metadata["cwd"] != opts.Cwd {
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

// --- file enumeration -------------------------------------------------------

type fileInfo struct {
	path string
	mod  time.Time
	id   string
}

// sessionFiles returns every dsh session log under <root>/sessions, newest
// first. The walk matches session.jsonl and session.jsonl.zstd by name rather
// than hard-coding the two directory levels, so a future nesting change costs
// nothing. The session id is its directory's base name.
func sessionFiles(root string) ([]fileInfo, error) {
	dir := filepath.Join(root, "sessions")
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var files []fileInfo
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if base := d.Name(); base != "session.jsonl" && base != "session.jsonl.zstd" {
			return nil
		}
		if info, e := d.Info(); e == nil {
			files = append(files, fileInfo{
				path: p,
				mod:  info.ModTime(),
				id:   filepath.Base(filepath.Dir(p)),
			})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	return files, err
}

// readMeta delegates instead of doing a metadata-only scan: dsh keeps the
// title and model in mid-file events, so a "lighter" read would parse the
// whole file anyway. Logs are small (a long session stays under a few MB).
func readMeta(fi fileInfo) (session.Source, error) {
	t, err := readThread(fi)
	return t.Source, err
}

// --- parsing ----------------------------------------------------------------

// The envelope decodes only fields present on (nearly) every line; data is
// kept raw and handed to per-type shapes in applyLine. A fat struct covering
// every event's fields cannot work here: unrelated events reuse field names
// with different shapes (session/title's source.model is an object describing
// the title model, while a message source's model is a string), and one
// mismatch would fail the whole line's decode.
type dshLine struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`  // session header only
	Cwd       string          `json:"cwd"` // session header only
	Time      int64           `json:"time"`
	SurfaceOp json.RawMessage `json:"surfaceOp"`
	Data      json.RawMessage `json:"data"`
}

// Per-event data shapes. Unmarshal failures are tolerated (the event then
// contributes nothing): a shape this provider does not understand is never
// visible timeline content.
type dshUserMessage struct {
	Content []dshBlock `json:"content"`
	Source  struct {
		Kind string `json:"kind"`
	} `json:"source"`
}

type dshAssistantMessage struct {
	Message struct {
		Content []dshBlock `json:"content"`
		Source  struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		} `json:"source"`
	} `json:"message"`
}

type dshBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func readThread(fi fileInfo) (session.Thread, error) {
	f, err := os.Open(fi.path)
	if err != nil {
		return session.Thread{}, err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(fi.path, ".zstd") {
		zr, err := zstd.NewReader(f)
		if err != nil {
			return session.Thread{}, fmt.Errorf("deepseek: open %s: %w", fi.path, err)
		}
		defer zr.Close()
		r = zr
	}

	src := session.Source{
		Ref:       session.Ref{Provider: session.ProviderDeepSeek, SessionID: fi.id},
		Path:      fi.path,
		UpdatedAt: fi.mod,
		Metadata:  map[string]string{},
	}

	var entries []session.Entry
	var warnings []string
	dec := json.NewDecoder(r)
	first := true
	for dec.More() {
		var line dshLine
		if err := dec.Decode(&line); err != nil {
			warnings = append(warnings, "stopped reading at a malformed record")
			break
		}
		if first {
			first = false
			if line.Type == "session" {
				if line.ID != "" {
					src.Ref.SessionID = line.ID
				}
				if line.Cwd != "" {
					src.Metadata["cwd"] = line.Cwd
				}
				continue
			}
		}
		applyLine(&src, &entries, line)
	}
	if src.Metadata["title"] == "" {
		if cwd := src.Metadata["cwd"]; cwd != "" {
			src.Metadata["title"] = filepath.Base(cwd)
		}
	}
	return session.Thread{Source: src, Entries: entries, Warnings: warnings}, nil
}

// applyLine folds one event into the source metadata or the timeline. Events
// are processed in file order, so "last writer wins" fields (title, model)
// land on the final value naturally. Each data shape is decoded
// independently; an unparseable or absent shape makes that event contribute
// nothing rather than fail the line.
func applyLine(src *session.Source, entries *[]session.Entry, line dshLine) {
	switch {
	case line.Type == "user/message":
		// Only rows a human typed (source.kind "user", appended to the
		// surface) are conversation. Plugin and skill-catalog rows are
		// injected context; a non-"append" surfaceOp marks a compaction
		// replacement, whose shape is unverified and skipped.
		var d dshUserMessage
		if json.Unmarshal(line.Data, &d) != nil {
			return
		}
		if d.Source.Kind != "user" || !isAppend(line.SurfaceOp) {
			return
		}
		if txt := blocksText(d.Content); txt != "" {
			*entries = append(*entries, session.Entry{
				Kind: session.KindMessage, Role: session.RoleUser,
				Text: txt, Time: msToTime(line.Time),
			})
		}
	case line.Type == "assistant/message":
		var d dshAssistantMessage
		if json.Unmarshal(line.Data, &d) != nil || !isAppend(line.SurfaceOp) {
			return
		}
		if txt := blocksText(d.Message.Content); txt != "" {
			*entries = append(*entries, session.Entry{
				Kind: session.KindMessage, Role: session.RoleAssistant,
				Text: txt, Time: msToTime(line.Time),
			})
		}
		if s := d.Message.Source; s.Model != "" || s.Provider != "" {
			setModel(src, s.Model, s.Provider)
		}
	case line.Type == "session/title":
		var d struct {
			Title string `json:"title"`
		}
		if json.Unmarshal(line.Data, &d) == nil && d.Title != "" {
			src.Metadata["title"] = d.Title
		}
	case line.Type == "request/header":
		var d struct {
			Header struct {
				Config struct {
					Provider string `json:"provider"`
					Model    string `json:"model"`
				} `json:"config"`
			} `json:"header"`
		}
		if json.Unmarshal(line.Data, &d) == nil {
			if c := d.Header.Config; c.Model != "" || c.Provider != "" {
				setModel(src, c.Model, c.Provider)
			}
		}
	case strings.HasPrefix(line.Type, "compaction/"):
		var d struct {
			Summary string `json:"summary"`
		}
		json.Unmarshal(line.Data, &d)
		*entries = append(*entries, session.Entry{
			Kind: session.KindCompact, Text: d.Summary, Time: msToTime(line.Time),
		})
	}
}

// isAppend reports whether an event joined the surface as a plain append.
// Absent counts as append: the badge is required on surface events, so its
// absence means an older or synthetic row, not a replacement.
func isAppend(op json.RawMessage) bool {
	return len(op) == 0 || string(op) == `"append"`
}

// setModel keeps the last writer. request/header (per model request) and the
// assistant message sources agree in practice; when they do not, the later
// event is the model that actually answered the later turns.
func setModel(src *session.Source, model, provider string) {
	if model != "" {
		src.Metadata["model"] = model
	}
	if provider != "" {
		src.Metadata["model_provider"] = provider
	}
}

// blocksText joins the text blocks of a content list. Assistant reasoning
// blocks and any non-text block are skipped.
func blocksText(blocks []dshBlock) string {
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func msToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
