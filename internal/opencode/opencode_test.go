package opencode

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/wilbeibi/catchup/internal/session"
)

// makeDB creates a minimal OpenCode database (the subset of columns catchup reads)
// from one of the schemas below and returns the root directory containing opencode.db.
func makeDB(t *testing.T, stmts []string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	return root
}

var legacyDB = []string{
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
	`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p3','m2','ses1',1110,'{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"ls"},"output":"error-looking success"}}')`,
	`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p3a','m2','ses1',1115,'{"type":"tool","tool":"bash","state":{"status":"error","input":{"command":"go test ./..."},"error":"FAIL proj"}}')`,
	`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p4','m2','ses1',1120,'{"type":"compaction"}')`,

	// older session
	`INSERT INTO session(id,directory,title,time_created,time_updated) VALUES ('ses2','/home/u/src/fsm','fsm review',500,900)`,
	`INSERT INTO message(id,session_id,time_created,data) VALUES ('m3','ses2',500,'{"role":"user"}')`,
	`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p5','m3','ses2',500,'{"type":"text","text":"unrelated"}')`,
}

// v2DB holds the same sessions in OpenCode 2.x tables. Row shapes follow
// session_message rows written by OpenCode 2.x, with their text replaced.
var v2DB = []string{
	`CREATE TABLE session_v2 (id TEXT PRIMARY KEY, project_id TEXT, parent_id TEXT, slug TEXT,
		directory TEXT, title TEXT, version TEXT, time_created INTEGER, time_updated INTEGER,
		time_archived INTEGER, agent TEXT, model TEXT)`,
	`CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT, type TEXT, seq INTEGER,
		time_created INTEGER, time_updated INTEGER, data TEXT)`,

	`INSERT INTO session_v2(id,directory,title,time_created,time_updated,agent,model) VALUES
		('ses1','/home/u/src/xurl','xurl design',1000,2000,'orchestrator','{"id":"v4","providerID":"deepseek","variant":"max"}')`,
	`INSERT INTO session_message(id,session_id,type,seq,time_created,data) VALUES
		('m1','ses1','user',0,1000,'{"text":"hello opencode","time":{"created":1000}}'),
		('m2','ses1','assistant',1,1100,'{"agent":"orchestrator","model":{"providerID":"deepseek","id":"v4"},"content":[
			{"type":"reasoning","text":"scratch","time":{"created":1100,"completed":1101}},
			{"type":"text","text":"hi there"},
			{"type":"tool","id":"call_1","name":"bash","state":{"status":"completed","input":{"command":"ls"},"content":[{"type":"text","text":"error-looking success"}]},"time":{"created":1110,"completed":1111}},
			{"type":"tool","id":"call_2","name":"bash","state":{"status":"error","input":{"command":"go test ./..."},"error":{"type":"tool.execution","message":"FAIL proj"}},"time":{"created":1115,"completed":1116}}
		],"finish":"tool-calls","time":{"created":1100}}'),
		('m3','ses1','synthetic',2,1118,'{"text":"The following tool was executed by the user","time":{"created":1118}}'),
		('m4','ses1','compaction',3,1120,'{"status":"completed","reason":"manual","summary":"## Goal\n- design xurl","recent":"","time":{"created":1120}}')`,

	`INSERT INTO session_v2(id,directory,title,time_created,time_updated) VALUES ('ses2','/home/u/src/fsm','fsm review',500,900)`,
	`INSERT INTO session_message(id,session_id,type,seq,time_created,data) VALUES ('m5','ses2','user',0,500,'{"text":"unrelated","time":{"created":500}}')`,
}

func TestListAndRead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		db      []string
		summary string
	}{
		{"legacy", legacyDB, ""},
		{"v2", v2DB, "## Goal\n- design xurl"},
	} {
		t.Run(tc.name, func(t *testing.T) { testListAndRead(t, tc.db, tc.summary) })
	}
}

func testListAndRead(t *testing.T, stmts []string, summary string) {
	roots := session.Roots{OpenCode: makeDB(t, stmts)}
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
		{session.KindMessage, session.RoleAssistant, "hi there"},
		{session.KindFailure, session.RoleTool, "FAIL proj"},
		{session.KindCompact, "", summary},
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
	if failure := th.Entries[2]; failure.Tool != "bash" || failure.Input != `{"command":"go test ./..."}` || !failure.Time.Equal(time.UnixMilli(1115)) {
		t.Errorf("failure = %+v, want structured bash failure at part time", failure)
	}
}

func TestQueryFilterAndResolveByID(t *testing.T) {
	for name, db := range map[string][]string{"legacy": legacyDB, "v2": v2DB} {
		t.Run(name, func(t *testing.T) { testQueryFilterAndResolveByID(t, db) })
	}
}

func testQueryFilterAndResolveByID(t *testing.T, stmts []string) {
	roots := session.Roots{OpenCode: makeDB(t, stmts)}
	p := New()
	ctx := context.Background()

	sums, err := p.List(ctx, roots, session.ListOptions{Query: "UNRELATED"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "ses2" {
		t.Fatalf("query filter failed: %+v", sums)
	}

	sums, err = p.List(ctx, roots, session.ListOptions{Query: "THERE"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "ses1" || sums[0].Match == nil || sums[0].Match.Role != session.RoleAssistant {
		t.Fatalf("assistant text query failed: %+v", sums)
	}

	src, err := p.Resolve(ctx, roots, "ses2")
	if err != nil {
		t.Fatal(err)
	}
	if src.Metadata["title"] != "fsm review" {
		t.Errorf("resolved wrong session: %v", src.Metadata)
	}
}
