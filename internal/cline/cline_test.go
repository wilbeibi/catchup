package cline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

const manifestJSON = `{
  "version": 1,
  "session_id": "1784300926570_lzgyo",
  "source": "cli",
  "started_at": "2026-07-17T15:08:46.576Z",
  "ended_at": "2026-07-17T15:08:51.773Z",
  "status": "completed",
  "provider": "deepseek",
  "model": "deepseek-chat",
  "cwd": "/home/u/src/catchup",
  "prompt": "support cline",
  "metadata": {"title": "Support cline in catchup"}
}`

const messagesJSON = `{
  "version": 1,
  "updated_at": "2026-07-17T15:08:51.755Z",
  "agent": "lead",
  "sessionId": "1784300926570_lzgyo",
  "system_prompt": "You are Cline",
  "messages": [
    {"id":"m1","role":"user","content":[{"type":"text","text":"<user_input mode=\"act\">support cline</user_input>"}],"ts":1784300926746},
    {"id":"m2","role":"assistant","content":[{"type":"text","text":"I'll inspect the format."},{"type":"tool_use","id":"c1","name":"editor","input":{"path":"x"}}],"ts":1784300928000},
    {"id":"m3","role":"user","content":[{"type":"tool_result","tool_use_id":"c1","name":"editor","content":"tool output"}],"ts":1784300929000},
    {"id":"m4","role":"user","content":"Context summary:\n\nsummary so far","metadata":{"kind":"compaction_summary","summary":"summary so far","tokensBefore":9000},"ts":1784300930000},
    {"id":"m5","role":"user","content":[{"type":"text","text":"<user_input mode=\"act\">finish it</user_input>"}],"ts":1784300931000},
    {"id":"m6","role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"done"}],"ts":1784300932000}
  ]
}`

func writeSession(t *testing.T, root, id, manifest, messages string) string {
	t.Helper()
	dir := filepath.Join(root, "data", "sessions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if messages != "" {
		if err := os.WriteFile(filepath.Join(dir, id+".messages.json"), []byte(messages), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestReadClineSession(t *testing.T) {
	root := t.TempDir()
	dir := writeSession(t, root, "1784300926570_lzgyo", manifestJSON, messagesJSON)
	// A sibling subagent transcript must not leak into the timeline.
	sub := `{"messages":[{"role":"user","content":[{"type":"text","text":"subagent noise"}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "1784300926570_lzgyo-sub1.messages.json"), []byte(sub), 0o644); err != nil {
		t.Fatal(err)
	}

	p := New()
	src, err := p.Resolve(context.Background(), session.Roots{Cline: root}, "1784300926570_lzgyo")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.Provider != session.ProviderCline || src.Ref.SessionID != "1784300926570_lzgyo" {
		t.Fatalf("source ref = %+v", src.Ref)
	}
	if src.Metadata["title"] != "Support cline in catchup" || src.Metadata["cwd"] != "/home/u/src/catchup" {
		t.Fatalf("metadata = %+v", src.Metadata)
	}
	if src.Metadata["model"] != "deepseek-chat" || src.Metadata["model_provider"] != "deepseek" {
		t.Fatalf("model metadata = %+v", src.Metadata)
	}

	thread, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	want := []session.Entry{
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "support cline"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "I'll inspect the format."},
		{Kind: session.KindCompact, Text: "summary so far"},
		{Kind: session.KindMessage, Role: session.RoleUser, Text: "finish it"},
		{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "done"},
	}
	if len(thread.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(thread.Entries), len(want), thread.Entries)
	}
	for i, w := range want {
		g := thread.Entries[i]
		if g.Kind != w.Kind || g.Role != w.Role || g.Text != w.Text {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}
	}
	for _, e := range thread.Entries {
		if strings.Contains(e.Text, "tool output") || strings.Contains(e.Text, "hidden") ||
			strings.Contains(e.Text, "user_input") || strings.Contains(e.Text, "subagent noise") {
			t.Fatalf("noise leaked into timeline: %+v", thread.Entries)
		}
	}
}

func TestListClineSessions(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "1784300926570_lzgyo", manifestJSON, messagesJSON)
	otherManifest := strings.ReplaceAll(manifestJSON, "/home/u/src/catchup", "/home/u/other")
	otherManifest = strings.ReplaceAll(otherManifest, "1784300926570_lzgyo", "1784300000000_other")
	otherMessages := strings.ReplaceAll(messagesJSON, "1784300926570_lzgyo", "1784300000000_other")
	dir := writeSession(t, root, "1784300000000_other", otherManifest, otherMessages)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "1784300000000_other.messages.json"), old, old); err != nil {
		t.Fatal(err)
	}

	p := New()
	sums, err := p.List(context.Background(), session.Roots{Cline: root}, session.ListOptions{
		Cwd:   "/home/u/src/catchup",
		Query: "finish",
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "1784300926570_lzgyo" {
		t.Fatalf("summaries = %+v", sums)
	}
	if sums[0].Rank != 1 || sums[0].Preview != "support cline" {
		t.Fatalf("summary fields = %+v", sums[0])
	}
}

func TestResolveNewestByTranscriptMtime(t *testing.T) {
	root := t.TempDir()
	// The "older by manifest" session has the fresher transcript, so it wins:
	// recency must track the transcript, not started_at.
	staleManifest := strings.ReplaceAll(manifestJSON, "1784300926570_lzgyo", "1784200000000_stale")
	staleMessages := strings.ReplaceAll(messagesJSON, "1784300926570_lzgyo", "1784200000000_stale")
	writeSession(t, root, "1784200000000_stale", staleManifest, staleMessages)
	dir := writeSession(t, root, "1784300926570_lzgyo", manifestJSON, messagesJSON)
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "1784300926570_lzgyo.messages.json"), old, old); err != nil {
		t.Fatal(err)
	}

	src, err := New().Resolve(context.Background(), session.Roots{Cline: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "1784200000000_stale" {
		t.Fatalf("newest = %+v", src.Ref)
	}
}
