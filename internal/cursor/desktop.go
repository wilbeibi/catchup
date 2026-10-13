package cursor

// Cursor 3.24.12 (2026-10-12): globalStorage/state.vscdb and the installed
// workbench.desktop.main.js UI exporter use ordered composer bubble headers.
// agent-transcripts JSONL omits mid-turn user messages and tool results.
// Compaction boundaries and the alternative headerLog encoding are unverified.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
	"github.com/wilbeibi/catchup/internal/sqlitedb"
)

type composer struct {
	ComposerID          string `json:"composerId"`
	Name                string `json:"name"`
	LastUpdatedAt       int64  `json:"lastUpdatedAt"`
	IsDraft             bool   `json:"isDraft"`
	WorkspaceIdentifier struct {
		URI struct {
			FSPath string `json:"fsPath"`
			Scheme string `json:"scheme"`
		} `json:"uri"`
	} `json:"workspaceIdentifier"`
	Headers []struct {
		BubbleID string `json:"bubbleId"`
	} `json:"fullConversationHeadersOnly"`
	HeaderLog json.RawMessage `json:"headerLog"`
}

func desktopSources(ctx context.Context, root, id string) ([]session.Source, error) {
	if root == "" {
		return nil, nil
	}
	path := filepath.Join(root, "User", "globalStorage", "state.vscdb")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	db, err := sqlitedb.Open(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	query, args := "SELECT value FROM composerHeaders WHERE isArchived=0 AND isSubagent=0", []any{}
	if id != "" {
		query, args = "SELECT value FROM composerHeaders WHERE composerId=?", []any{strings.TrimPrefix(id, "ide:")}
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("cursor IDE headers: %w", err)
	}
	defer rows.Close()
	var out []session.Source
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var c composer
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("cursor IDE header: %w", err)
		}
		if c.ComposerID == "" || (id == "" && c.IsDraft) {
			continue
		}
		cwd := ""
		if c.WorkspaceIdentifier.URI.Scheme == "file" {
			cwd = c.WorkspaceIdentifier.URI.FSPath
		}
		out = append(out, session.Source{
			Ref:  session.Ref{Provider: session.ProviderCursor, SessionID: "ide:" + c.ComposerID},
			Path: path, UpdatedAt: time.UnixMilli(c.LastUpdatedAt),
			Metadata: map[string]string{"title": c.Name, "cwd": cwd},
		})
	}
	return out, rows.Err()
}

type bubble struct {
	Type      int    `json:"type"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
	Tool      *struct {
		Name    string `json:"name"`
		RawArgs string `json:"rawArgs"`
		Result  string `json:"result"`
	} `json:"toolFormerData"`
}

func readDesktop(ctx context.Context, src session.Source) (session.Thread, error) {
	db, err := sqlitedb.Open(src.Path)
	if err != nil {
		return session.Thread{}, err
	}
	defer db.Close()
	// The composer and its bubbles must belong to the same WAL snapshot.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return session.Thread{}, err
	}
	defer tx.Rollback()
	read := func(key string, dst any) error {
		var raw []byte
		if err := tx.QueryRowContext(ctx, "SELECT value FROM cursorDiskKV WHERE key=?", key).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, dst)
	}
	id := strings.TrimPrefix(src.Ref.SessionID, "ide:")
	var c composer
	if err := read("composerData:"+id, &c); err != nil {
		return session.Thread{}, fmt.Errorf("cursor IDE conversation: %w", err)
	}
	if len(c.HeaderLog) != 0 && string(c.HeaderLog) != "null" {
		return session.Thread{}, fmt.Errorf("cursor IDE: unsupported headerLog history encoding")
	}
	t := session.Thread{Source: src, Warnings: []string{"Cursor IDE compaction boundaries and API stop events are unavailable"}}
	var unknown session.UnknownTypes
	for _, h := range c.Headers {
		var b bubble
		if err := read("bubbleId:"+id+":"+h.BubbleID, &b); err != nil {
			t.Warnings = append(t.Warnings, fmt.Sprintf("skipped bubble %s: %v", h.BubbleID, err))
			continue
		}
		role := session.RoleUser
		switch b.Type {
		case 1:
		case 2:
			role = session.RoleAssistant
		default:
			unknown.Add(fmt.Sprintf("bubble type %d", b.Type))
			continue
		}
		at := session.ParseTime(b.CreatedAt)
		if b.Text != "" {
			t.Entries = append(t.Entries, session.Entry{Kind: session.KindMessage, Role: role, Text: b.Text, Time: at})
		}
		if tool := b.Tool; tool != nil && tool.Name != "" {
			input := json.RawMessage(tool.RawArgs)
			t.Entries = append(t.Entries, session.ToolCall(tool.Name, input, at))
			var result struct {
				ExitCode int  `json:"exitCode"`
				Rejected bool `json:"rejected"`
			}
			if json.Unmarshal([]byte(tool.Result), &result) == nil && (result.ExitCode != 0 || result.Rejected) {
				t.Entries = append(t.Entries, session.Failure(tool.Name, input, tool.Result, at))
			}
		}
	}
	t.Warnings = unknown.AppendTo(t.Warnings)
	return t, nil
}
