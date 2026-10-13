// Package grokbot reads Grok Bot's local transcript replica cache, not Grok
// Build or ~/.grokbot's execution daemon. Evidence: Grok Bot 0.70.2 persistence
// files and installed Electron bundle, inspected 2026-10-12. Schema 1 replicas
// retain at most 200 entries / 768 KiB of JS string data; only 24 recent replicas
// survive eviction. The cloud transcript, tool failures and compaction history
// cannot be reconstructed from this cache. Other schema versions are unverified.
package grokbot

import (
	"bytes"
	"context"
	"encoding/base32"
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

var _ session.Provider = (*Provider)(nil)

func (p *Provider) Resolve(ctx context.Context, roots session.Roots, id string) (session.Source, error) {
	threads, err := p.sessions(ctx, roots.GrokBot)
	if err != nil {
		return session.Source{}, err
	}
	for _, t := range threads {
		if id == "" || t.Source.Ref.SessionID == id {
			return t.Source, nil
		}
	}
	return session.Source{}, fmt.Errorf("grokbot: no cached conversation with id %q", id)
}

func (p *Provider) List(ctx context.Context, roots session.Roots, opts session.ListOptions) ([]session.Summary, error) {
	// Roster paths name remote bot databases, never a local project directory.
	if opts.Cwd != "" {
		return nil, nil
	}
	threads, err := p.sessions(ctx, roots.GrokBot)
	if err != nil {
		return nil, err
	}
	var out []session.Summary
	for _, t := range threads {
		if len(t.Entries) == 0 || !opts.MatchesQuery(t) {
			continue
		}
		s := opts.Summarize(t)
		s.Rank = len(out) + 1
		out = append(out, s)
		if len(out) == opts.EffectiveLimit() {
			break
		}
	}
	return out, nil
}

func (p *Provider) sessions(ctx context.Context, root string) ([]session.Thread, error) {
	if root == "" {
		return nil, nil
	}
	dir := filepath.Join(root, "sand-client-persistence")
	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	accountKey := "sand.client.slice.client-meta.account-slot"
	var slot string
	err := decode(filepath.Join(dir, strings.ToLower(encoding.EncodeToString([]byte(accountKey)))+".blob"), &slot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []session.Thread
	for _, f := range files {
		path := filepath.Join(dir, f.Name())
		var key string
		switch filepath.Ext(f.Name()) {
		case ".blob":
			decoded, err := encoding.DecodeString(strings.ToUpper(strings.TrimSuffix(f.Name(), ".blob")))
			if err != nil {
				continue
			}
			key = string(decoded)
		case ".kblob":
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			line, _, _ := bytes.Cut(data, []byte{'\n'})
			if err := json.Unmarshal(line, &key); err != nil {
				return nil, err
			}
		default:
			continue // includes in-progress atomic-write temporary files
		}
		account, suffix, ok := strings.Cut(strings.TrimPrefix(key, "sand.client.slice.account."), ".")
		decoded, err := url.PathUnescape(account)
		if !ok || err != nil || decoded != slot || !strings.HasPrefix(suffix, "transcript.replicas.") {
			continue
		}
		src := session.Source{Ref: session.Ref{Provider: session.ProviderGrokBot, SessionID: strings.TrimPrefix(suffix, "transcript.replicas.")}, Path: path}
		t, err := p.Read(ctx, src)
		if err != nil {
			return nil, fmt.Errorf("grokbot replica %s: %w", src.Ref.SessionID, err)
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Source.UpdatedAt.After(out[j].Source.UpdatedAt) })
	return out, nil
}

// Long keys use a hash filename and a JSON-quoted key line before the envelope.
func decode(path string, dst any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if filepath.Ext(path) == ".kblob" {
		_, data, _ = bytes.Cut(data, []byte{'\n'})
	}
	var envelope struct {
		SchemaVersion int             `json:"schemaVersion"`
		Value         json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if envelope.SchemaVersion != 1 {
		return fmt.Errorf("unsupported Grok Bot cache schema %d", envelope.SchemaVersion)
	}
	return json.Unmarshal(envelope.Value, dst)
}

func (p *Provider) Read(ctx context.Context, src session.Source) (session.Thread, error) {
	var replica struct {
		PrecedingTimestampMs int64 `json:"precedingTimestampMs"`
		Entries              []struct {
			Kind        string                         `json:"kind"`
			Role        string                         `json:"role"`
			Content     string                         `json:"content"`
			TimestampMs int64                          `json:"timestampMs"`
			FromAgent   *json.RawMessage               `json:"fromAgent"`
			ToAgent     *json.RawMessage               `json:"toAgent"`
			FileName    string                         `json:"file_name"`
			Message     struct{ Type, Content string } `json:"message"`
			Author      struct{ ID, Name string }      `json:"author"`
		} `json:"entries"`
	}
	if err := decode(src.Path, &replica); err != nil {
		return session.Thread{}, err
	}
	t := session.Thread{Source: src, Warnings: []string{"Grok Bot local cache may omit older messages, tool failures, non-text content and compaction history"}}
	if replica.PrecedingTimestampMs > 0 {
		t.Warnings = append(t.Warnings, "earlier messages were truncated from this replica")
	}
	var unknown session.UnknownTypes
	for _, r := range replica.Entries {
		if err := ctx.Err(); err != nil {
			return session.Thread{}, err
		}
		at := time.Time{}
		if r.TimestampMs > 0 {
			at = time.UnixMilli(r.TimestampMs)
		}
		if at.After(t.Source.UpdatedAt) {
			t.Source.UpdatedAt = at // persistedAt measures cache refresh, not activity
		}
		role, text := r.Role, r.Content
		switch r.Kind {
		case "message":
			if r.FromAgent != nil || r.ToAgent != nil || (role != "user" && role != "assistant") {
				unknown.Add("message/" + role)
				continue
			}
		case "send-message":
			switch r.Message.Type {
			case "text":
			case "widget", "connector", "user-form", "auto-review-approval", "local-tool-permission", "secret-request":
				continue
			default:
				unknown.Add("send-message/" + r.Message.Type)
				continue
			}
			role, text = session.RoleAssistant, r.Message.Content
			if r.Author.Name != "" {
				text = r.Author.Name + ": " + text
			}
		case "user-attachment":
			role, text = session.RoleUser, "[attachment: "+r.FileName+"]"
		default:
			unknown.Add(r.Kind)
			continue
		}
		if text != "" {
			t.Entries = append(t.Entries, session.Entry{Kind: session.KindMessage, Role: role, Text: text, Time: at})
		}
	}
	t.Warnings = unknown.AppendTo(t.Warnings)
	return t, nil
}
