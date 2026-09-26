// Package amp reads locally cached Amp threads. It never contacts Amp's server.
// Wire shapes: https://github.com/block/thread-manager-for-amp/blob/main/server/lib/threadTypes.ts
package amp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

type Provider struct{}

func New() *Provider { return &Provider{} }

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	sources, err := sources(roots.Amp)
	if err != nil {
		return session.Source{}, err
	}
	for _, src := range sources {
		t, err := p.Read(ctx, src)
		if err == nil && (id == "" || t.Source.Ref.SessionID == id) {
			return t.Source, nil
		}
	}
	return session.Source{}, fmt.Errorf("amp: no locally cached thread %q; only files under %s/threads are available", id, roots.Amp)
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	sources, err := sources(roots.Amp)
	if err != nil {
		return nil, err
	}
	out := []session.Summary{}
	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t, err := p.Read(ctx, src)
		if err != nil || !opts.Matches(t) {
			continue
		}
		s := opts.Summarize(t)
		s.Rank = len(out) + 1
		out = append(out, s)
		if len(out) >= opts.EffectiveLimit() {
			break
		}
	}
	return out, nil
}

func sources(root string) ([]session.Source, error) {
	if root == "" {
		return nil, nil
	}
	files, err := os.ReadDir(filepath.Join(root, "threads"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []session.Source
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		out = append(out, session.Source{Path: filepath.Join(root, "threads", f.Name()), UpdatedAt: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].Path < out[j].Path
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

type threadFile struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Meta    struct {
			SentAt int64 `json:"sentAt"`
		} `json:"meta"`
	} `json:"messages"`
	Relationships []struct {
		Type     string `json:"type"`
		Role     string `json:"role"`
		ThreadID string `json:"threadID"`
	} `json:"relationships"`
	Env struct {
		Initial struct {
			Trees []struct {
				URI string `json:"uri"`
			} `json:"trees"`
		} `json:"initial"`
	} `json:"env"`
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	if err := ctx.Err(); err != nil {
		return session.Thread{}, err
	}
	raw, err := os.ReadFile(src.Path)
	if err != nil {
		return session.Thread{}, err
	}
	var wire threadFile
	if err := json.Unmarshal(raw, &wire); err != nil {
		return session.Thread{}, fmt.Errorf("amp: read %s: %w", src.Path, err)
	}
	if wire.ID == "" {
		return session.Thread{}, fmt.Errorf("amp: %s has no thread id", src.Path)
	}
	src.Ref = session.Ref{Provider: session.ProviderAmp, SessionID: wire.ID}
	src.Metadata = map[string]string{"title": wire.Title, "native_title": wire.Title, "url": "https://ampcode.com/threads/" + wire.ID}
	for _, tree := range wire.Env.Initial.Trees {
		uri, err := url.Parse(tree.URI)
		if err == nil && uri.Scheme == "file" && (uri.Host == "" || uri.Host == "localhost") {
			dir := uri.Path
			if len(dir) >= 3 && dir[0] == '/' && dir[2] == ':' {
				dir = dir[1:]
			}
			src.Metadata["cwd"] = filepath.FromSlash(dir)
			break
		}
	}
	for _, rel := range wire.Relationships {
		// Role describes this thread, not the referenced thread.
		if rel.Type == "handoff" && rel.Role == "child" && rel.ThreadID != "" {
			src.Metadata["parent"], src.Metadata["relationship"] = rel.ThreadID, rel.Type
			break
		}
	}
	t := session.Thread{Source: src}
	for _, m := range wire.Messages {
		if m.Role != session.RoleUser && m.Role != session.RoleAssistant {
			continue
		}
		text := visibleText(m.Content)
		if text != "" {
			at := time.Time{}
			if m.Meta.SentAt != 0 {
				at = time.UnixMilli(m.Meta.SentAt)
			}
			t.Entries = append(t.Entries, session.Entry{Kind: session.KindMessage, Role: m.Role, Text: text, Time: at})
		}
	}
	return t, nil
}

// Text may be a string or an array of strings and typed blocks. Reasoning,
// images and tool payloads are not conversation text.
func visibleText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, raw := range blocks {
		if json.Unmarshal(raw, &text) == nil {
			parts = append(parts, text)
			continue
		}
		var b struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &b) == nil && b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
