package zcode

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/wilbeibi/catchup/internal/session"
)

// makeDB creates a minimal ZCode database (the subset of columns catchup reads)
// and returns the root directory containing db.sqlite. The schema mirrors the
// real ZCode 3.x install: session/message/part with epoch-ms times, content
// blocks in part.data JSON, and time_archived distinguishing the trash.
func makeDB(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "db.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, title TEXT,
			time_created INTEGER, time_updated INTEGER, time_compacting INTEGER,
			time_archived INTEGER, parent_id TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER,
			time_updated INTEGER, data TEXT, sequence INTEGER)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT,
			time_created INTEGER, time_updated INTEGER, data TEXT, sequence INTEGER)`,

		// newest session — assistant carries flat modelID/providerID
		`INSERT INTO session(id,directory,title,time_created,time_updated) VALUES
			('ses1','/home/u/src/xurl','xurl design',1000,2000)`,
		`INSERT INTO message(id,session_id,time_created,sequence,data) VALUES
			('m1','ses1',1000,0,'{"role":"user","model":{"modelID":"GLM-5.2","providerID":"builtin:zai-coding-plan"}}')`,
		`INSERT INTO message(id,session_id,time_created,sequence,data) VALUES
			('m2','ses1',1100,1,'{"role":"assistant","modelID":"GLM-5.2","providerID":"builtin:zai-coding-plan"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p1','m1','ses1',1000,'{"type":"text","text":"hello zcode"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p2','m2','ses1',1100,'{"type":"text","text":"hi there"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p3','m2','ses1',1110,'{"type":"tool","tool":"bash"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p4','m2','ses1',1120,'{"type":"reasoning","text":"hidden"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p5','m2','ses1',1130,'{"type":"compaction","compactReason":"user_requested"}')`,

		// older session
		`INSERT INTO session(id,directory,title,time_created,time_updated) VALUES ('ses2','/home/u/src/fsm','fsm review',500,900)`,
		`INSERT INTO message(id,session_id,time_created,sequence,data) VALUES ('m3','ses2',500,0,'{"role":"user"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p6','m3','ses2',500,'{"type":"text","text":"unrelated"}')`,

		// archived (trash) session — must never appear
		`INSERT INTO session(id,directory,title,time_created,time_updated,time_archived) VALUES
			('ses3','/home/u/src/trash','gone',300,400,400)`,
		`INSERT INTO message(id,session_id,time_created,sequence,data) VALUES ('m4','ses3',300,0,'{"role":"user"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('p7','m4','ses3',300,'{"type":"text","text":"should not show"}')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	return root
}

func TestListAndRead(t *testing.T) {
	roots := session.Roots{ZCode: makeDB(t)}
	p := New()
	ctx := context.Background()

	sums, err := p.List(ctx, roots, session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 { // ses3 is archived and must be excluded
		t.Fatalf("got %d summaries, want 2: %+v", len(sums), sums)
	}
	if sums[0].Rank != 1 || sums[0].Ref.SessionID != "ses1" {
		t.Errorf("newest should rank 1: %+v", sums[0])
	}
	if sums[0].Preview != "hello zcode" {
		t.Errorf("preview = %q", sums[0].Preview)
	}

	src, err := p.Resolve(ctx, roots, "")
	if err != nil {
		t.Fatal(err)
	}

	th, err := p.Read(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	// model lives in the transcript (message.data), so Read enriches it —
	// not Resolve. See the session.Provider.Read contract.
	if th.Source.Metadata["model"] != "GLM-5.2" || th.Source.Metadata["model_provider"] != "builtin:zai-coding-plan" {
		t.Errorf("model metadata = %v", th.Source.Metadata)
	}
	want := []struct{ kind, role, text string }{
		{session.KindMessage, session.RoleUser, "hello zcode"},
		{session.KindMessage, session.RoleAssistant, "hi there"}, // tool/reasoning parts dropped
		{session.KindCompact, "", "user_requested"},
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

func TestArchivedExcluded(t *testing.T) {
	roots := session.Roots{ZCode: makeDB(t)}
	p := New()
	ctx := context.Background()

	// Archived session is not listable...
	sums, err := p.List(ctx, roots, session.ListOptions{Query: "should not show"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 0 {
		t.Fatalf("archived session leaked into listing: %+v", sums)
	}
	// ...nor resolvable by id.
	if _, err := p.Resolve(ctx, roots, "ses3"); err == nil {
		t.Fatal("expected error resolving archived session, got nil")
	}
}

func TestQueryFilterAndResolveByID(t *testing.T) {
	roots := session.Roots{ZCode: makeDB(t)}
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

// makeMixedModelDB builds a session whose assistant messages alternate a
// canonical provider id ("builtin:zai-coding-plan", "GLM-5.2") with an
// internal/bridge variant (bare UUID provider, "glm-5.2[1m]"). The canonical
// form must win regardless of order.
func makeMixedModelDB(t *testing.T, canonicalLast bool) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "db.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT, title TEXT,
			time_created INTEGER, time_updated INTEGER, time_compacting INTEGER,
			time_archived INTEGER, parent_id TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER,
			time_updated INTEGER, data TEXT, sequence INTEGER)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT,
			time_created INTEGER, time_updated INTEGER, data TEXT, sequence INTEGER)`,
		`INSERT INTO session(id,directory,title,time_created,time_updated) VALUES ('sm','/p','m',1000,4000)`,
		`INSERT INTO message(id,session_id,time_created,sequence,data) VALUES ('mu','sm',1000,0,'{"role":"user"}')`,
		`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('pu','mu','sm',1000,'{"type":"text","text":"q"}')`,
	}
	canon := `INSERT INTO message(id,session_id,time_created,sequence,data) VALUES ('mc','sm',2000,1,'{"role":"assistant","modelID":"GLM-5.2","providerID":"builtin:zai-coding-plan"}')`
	variant := `INSERT INTO message(id,session_id,time_created,sequence,data) VALUES ('mv','sm',3000,2,'{"role":"assistant","modelID":"glm-5.2[1m]","providerID":"a241de91-6e05-41dc-af42-ae0760d5e579"}')`
	if canonicalLast {
		stmts = append(stmts, variant, canon,
			`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('pv','mv','sm',3000,'{"type":"text","text":"a1"}')`,
			`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('pc','mc','sm',2000,'{"type":"text","text":"a2"}')`)
	} else {
		stmts = append(stmts, canon, variant,
			`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('pc','mc','sm',2000,'{"type":"text","text":"a2"}')`,
			`INSERT INTO part(id,message_id,session_id,time_created,data) VALUES ('pv','mv','sm',3000,'{"type":"text","text":"a1"}')`)
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	return root
}

func TestModelPrefersCanonicalProvider(t *testing.T) {
	for _, canonicalLast := range []bool{false, true} {
		name := "canonicalFirst"
		if canonicalLast {
			name = "canonicalLast"
		}
		t.Run(name, func(t *testing.T) {
			roots := session.Roots{ZCode: makeMixedModelDB(t, canonicalLast)}
			p := New()
			src, err := p.Resolve(context.Background(), roots, "")
			if err != nil {
				t.Fatal(err)
			}
			th, err := p.Read(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			if th.Source.Metadata["model"] != "GLM-5.2" || th.Source.Metadata["model_provider"] != "builtin:zai-coding-plan" {
				t.Fatalf("model = %v, want canonical GLM-5.2/builtin:zai-coding-plan", th.Source.Metadata)
			}
		})
	}
}
