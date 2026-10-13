package grokbot

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wilbeibi/catchup/internal/session"
)

// Sanitized shapes from Grok Bot 0.70.2 replicas on 2026-10-12. Ordering,
// authors and envelopes come from disk; hashed-key framing and truncation
// metadata come from its installed Electron persistence implementation.
func TestCachedConversations(t *testing.T) {
	for _, slot := range []string{"team.test@example", strings.Repeat("a", 120) + "@example"} {
		t.Run(slot[:9], func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "sand-client-persistence")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(key, body string) string {
				t.Helper()
				enc := base32.StdEncoding.WithPadding(base32.NoPadding)
				name := strings.ToLower(enc.EncodeToString([]byte(key))) + ".blob"
				if len(name) > 240 {
					digest := sha256.Sum256([]byte(key))
					name = strings.ToLower(enc.EncodeToString(digest[:])) + ".kblob"
					quoted, _ := json.Marshal(key)
					body = string(quoted) + "\n" + body
				}
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			}
			quoted, _ := json.Marshal(slot)
			write("sand.client.slice.client-meta.account-slot", `{"schemaVersion":1,"value":`+string(quoted)+`}`)
			prefix := "sand.client.slice.account." + strings.ReplaceAll(url.QueryEscape(slot), ".", "%2E") + ".transcript.replicas."
			write("sand.client.slice.account.other.transcript.replicas.private", `{"schemaVersion":1,"value":{"entries":[{"kind":"message","role":"user","content":"other account","timestampMs":9999999999999}]}}`)
			write(prefix+"old", `{"schemaVersion":1,"value":{"persistedAt":9999999999999,"entries":[{"kind":"message","role":"user","content":"older chat","timestampMs":1000}]}}`)
			path := write(prefix+"group", `{"schemaVersion":1,"value":{"epochHint":1,"acceptedSequenceHint":20,"precedingTimestampMs":500,"entries":[
				{"kind":"message","id":"u1","role":"user","content":"explain this","fromAgent":null,"toAgent":null,"timestampMs":2000,"clientNonce":"n1","seq":10},
				{"kind":"send-message","id":"a1","message":{"type":"text","content":"Here is the plan"},"timestampMs":3000,"requestId":"r1","seq":11,"author":{"id":"bot1","name":"Planner"}},
				{"kind":"message","id":"u2","role":"user","content":"follow-up","timestampMs":4000,"seq":15},
				{"kind":"user-attachment","id":"file1","file_name":"diagram.png","file_path":"/remote/diagram.png","timestampMs":5000,"seq":16},
				{"kind":"send-message","id":"form1","message":{"type":"secret-request","content":"do not export"},"timestampMs":5000,"seq":17},
				{"kind":"message","role":"user","fromAgent":{"id":"bot2"},"content":"not human","timestampMs":5000,"seq":18},
				{"kind":"future-kind","timestampMs":5000,"seq":20}
			]}}`)
			ctx, p, roots := context.Background(), New(), session.Roots{GrokBot: root}
			rows, err := p.List(ctx, roots, session.ListOptions{})
			if err != nil || len(rows) != 2 || rows[0].Ref.SessionID != "group" || rows[0].Cwd != "" || rows[1].Ref.SessionID != "old" {
				t.Fatalf("rows = %+v, %v", rows, err)
			}
			src, err := p.Resolve(ctx, roots, "")
			if err != nil || src.Path != path {
				t.Fatalf("resolve = %+v, %v", src, err)
			}
			thread, err := p.Read(ctx, src)
			if err != nil {
				t.Fatal(err)
			}
			if len(thread.Entries) != 4 || thread.Entries[0].Role != "user" || thread.Entries[1].Role != "assistant" || thread.Entries[1].Text != "Planner: Here is the plan" || thread.Entries[2].Text != "follow-up" || thread.Entries[3].Text != "[attachment: diagram.png]" || !thread.Source.UpdatedAt.Equal(time.UnixMilli(5000)) {
				t.Fatalf("thread = %+v", thread)
			}
			warnings := strings.Join(thread.Warnings, "\n")
			if !strings.Contains(warnings, "truncated") || !strings.Contains(warnings, "future-kind") {
				t.Fatalf("warnings = %s", warnings)
			}
			rows, err = p.List(ctx, roots, session.ListOptions{Cwd: "/remote"})
			if err != nil || len(rows) != 0 {
				t.Fatalf("project rows = %+v, %v", rows, err)
			}
			rows, err = p.List(ctx, roots, session.ListOptions{Query: "follow-up"})
			if err != nil || len(rows) != 1 {
				t.Fatalf("query rows = %+v, %v", rows, err)
			}
			if _, err := p.Resolve(ctx, roots, "private"); err == nil {
				t.Fatal("resolved another account")
			}
			write(prefix+"group", `{"schemaVersion":2,"value":{}}`)
			if _, err := p.Read(ctx, src); err == nil {
				t.Fatal("accepted unverified schema")
			}
			selected, err := p.Resolve(ctx, roots, "old")
			if err != nil || selected.Ref.SessionID != "old" {
				t.Fatalf("healthy replica blocked by another schema: %+v, %v", selected, err)
			}
			if _, err := p.Resolve(ctx, roots, "group"); err == nil {
				t.Fatal("explicit selection accepted unverified schema")
			}
		})
	}
}
