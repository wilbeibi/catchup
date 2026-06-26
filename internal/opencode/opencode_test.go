package opencode

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/wilbeibi/baton/internal/session"
)

// makeDB creates a minimal OpenCode database (the subset of columns baton reads)
// and returns the root directory containing opencode.db.
func makeDB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT, parent_id TEXT, slug TEXT,
			directory TEXT, title TEXT, version TEXT, time_created INTEGER, time_updated INTEGER,
			time_archived INTEGER, agent TEXT, model TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)`,

		// newest session
		`INSERT INTO session(id,directory,title,time_created,time_updated,agent,model) VALUES
			('ses1','/home/u/src/xurl','xurl design',1000,2000,'orchestrator','{"id":"v4","providerID":"deepseek"}')`,
		`INSERT INTO message(id,session_id,time_created,data) VALUES ('m1','ses1',1000,'{"role":"user"}')`,
		`INSERT INTO message(id,session_id,time_created,data) VALUES ('m2','ses1',1100,'{"role":"assistant"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p1','m1','ses1',1000,'{"type":"text","text":"hello opencode"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p2','m2','ses1',1100,'{"type":"text","text":"hi there"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p3','m2','ses1',1110,'{"type":"tool","tool":"bash"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p4','m2','ses1',1120,'{"type":"compaction"}')`,

		// older session
		`INSERT INTO session(id,directory,title,time_created,time_updated) VALUES ('ses2','/home/u/src/fsm','fsm review',500,900)`,
		`INSERT INTO message(id,session_id,time_created,data) VALUES ('m3','ses2',500,'{"role":"user"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p5','m3','ses2',500,'{"type":"text","text":"unrelated"}')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	return root
}

func TestListAndRead(t *testing.T) {
	roots := session.Roots{OpenCode: makeDB(t)}
	p := New()
	ctx := context.Background()

	sums, err := p.List(ctx, roots, session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 {
		t.Fatalf("got %d summaries, want 2", len(sums))
	}
	if sums[0].Rank != 1 || sums[0].Ref.SessionID != "ses1" {
		t.Errorf("newest should rank 1: %+v", sums[0])
	}
	if sums[0].Preview != "hello opencode" {
		t.Errorf("preview = %q", sums[0].Preview)
	}

	src, err := p.Resolve(ctx, roots, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Metadata["model"] != "deepseek/v4" || src.Metadata["agent"] != "orchestrator" {
		t.Errorf("metadata = %v", src.Metadata)
	}

	th, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ kind, role, text string }{
		{session.KindMessage, session.RoleUser, "hello opencode"},
		{session.KindMessage, session.RoleAssistant, "hi there"}, // tool part dropped
		{session.KindCompact, "", ""},
	}
	if len(th.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(th.Entries), len(want), th.Entries)
	}
	for i, w := range want {
		got := th.Entries[i]
		if got.Kind != w.kind || got.Role != w.role || got.Text != w.text {
			t.Errorf("entry %d = %+v, want %v", i, got, w)
		}
	}
}

func TestQueryFilterAndResolveByID(t *testing.T) {
	roots := session.Roots{OpenCode: makeDB(t)}
	p := New()
	ctx := context.Background()

	sums, err := p.List(ctx, roots, session.ListOptions{Query: "UNRELATED"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "ses2" {
		t.Fatalf("query filter failed: %+v", sums)
	}

	src, err := p.Resolve(ctx, roots, "ses2")
	if err != nil {
		t.Fatal(err)
	}
	if src.Metadata["title"] != "fsm review" {
		t.Errorf("resolved wrong session: %v", src.Metadata)
	}
}
