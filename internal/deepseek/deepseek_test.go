package deepseek

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/wilbeibi/catchup/internal/session"
)

// transcript mirrors a real dsh session log: header first, then append-only
// events. The plugin- and skill-catalog-sourced user rows, the packed chunk
// row, tool plumbing, and the compaction replacement are all noise the
// provider must skip.
const transcript = `{"type":"session","version":0,"id":"session-abc","createdAt":1786870675123,"cwd":"/home/u/src/xurl","delegationDepth":0,"agentPreset":"standard"}
{"type":"permission/preset","seq":0,"time":1786870675375,"data":{"preset":"workspace-write"}}
{"type":"turn/start","seq":1,"time":1786871592619,"data":{"turn":1}}
{"type":"step/start","seq":2,"time":1786871592687,"data":{"turn":1,"step":1}}
{"type":"request/header","seq":3,"time":1786871592688,"data":{"header":{"config":{"provider":"deepseek-official","model":"deepseek-v4-flash","reasoningEffort":"high","maxTokens":256000}}}}
{"type":"user/message","seq":4,"time":1786871592688,"data":{"content":[{"type":"text","text":"support dsh"}],"source":{"kind":"user","rpcId":"rpc-1"},"role":"user","id":"u1"},"surfaceOp":"append"}
{"type":"user/message","seq":5,"time":1786871592691,"data":{"content":[{"type":"text","text":"Current runtime context. Sandbox snapshot."}],"source":{"kind":"plugin","plugin":"@deepseek-ai/dsh-system-prompt","form":"snapshot"},"role":"user","id":"u2"},"surfaceOp":"append"}
{"type":"user/message","seq":6,"time":1786871592693,"data":{"content":[{"type":"text","text":"<system-reminder>skills catalog</system-reminder>"}],"source":{"kind":"skill-catalog"},"role":"user","id":"u3"},"surfaceOp":"append"}
{"type":"text-chunks","seq0":7,"time0":1786871595000,"data":{"chunks":[]}}
{"type":"assistant/message","seq":10,"time":1786871595652,"data":{"turn":1,"step":1,"message":{"content":[{"type":"reasoning","text":"hidden"},{"type":"text","text":"I will inspect the format."}],"role":"assistant","id":"a1","source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"}}},"surfaceOp":"append"}
{"type":"tool/call","seq":11,"time":1786871595700,"data":{"turn":1,"step":2,"callId":"c1","name":"bash","arguments":"{\"command\":\"ls\"}"}}
{"type":"tool/result","seq":12,"time":1786871595800,"data":{"turn":1,"step":2,"callId":"c1","content":[]}}
{"type":"assistant/message","seq":13,"time":1786871595900,"data":{"turn":1,"step":2,"message":{"content":[{"type":"text","text":"done"}],"role":"assistant","id":"a2","source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"}}},"surfaceOp":"append"}
{"type":"step/end","seq":14,"time":1786871595950,"data":{"turn":1,"step":2}}
{"type":"turn/end","seq":15,"time":1786871595951,"data":{"turn":1,"reason":"end_turn"}}
{"type":"session/title","seq":16,"time":1786871596000,"data":{"title":"dsh support","messageSeqs":[4],"source":{"kind":"provider","provider":"session-title-first-prompt-llm","model":{"provider":"deepseek-official","model":"deepseek-v4-flash"}}}}
{"type":"compaction/checkpoint","seq":17,"time":1786871600000,"data":{"summary":"summary so far"}}
{"type":"user/message","seq":18,"time":1786871600100,"data":{"content":[{"type":"text","text":"compacted replacement"}],"source":{"kind":"plugin","plugin":"compaction"},"role":"user","id":"u4"},"surfaceOp":{"op":"replace","shadowed":[4]}}
{"type":"user/message","seq":19,"time":1786871600200,"data":{"content":[{"type":"text","text":"finish it"}],"source":{"kind":"user","rpcId":"rpc-2"},"role":"user","id":"u5"},"surfaceOp":"append"}
{"type":"assistant/message","seq":20,"time":1786871600300,"data":{"turn":2,"step":1,"message":{"content":[{"type":"text","text":"first part"},{"type":"text","text":"second part"}],"role":"assistant","id":"a3","source":{"kind":"model","provider":"deepseek-official","model":"deepseek-v4-flash"}}},"surfaceOp":"append"}
`

var wantEntries = []session.Entry{
	{Kind: session.KindMessage, Role: session.RoleUser, Text: "support dsh", Time: time.UnixMilli(1786871592688)},
	{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "I will inspect the format.", Time: time.UnixMilli(1786871595652)},
	{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "done", Time: time.UnixMilli(1786871595900)},
	{Kind: session.KindCompact, Text: "summary so far", Time: time.UnixMilli(1786871600000)},
	{Kind: session.KindMessage, Role: session.RoleUser, Text: "finish it", Time: time.UnixMilli(1786871600200)},
	{Kind: session.KindMessage, Role: session.RoleAssistant, Text: "first part\nsecond part", Time: time.UnixMilli(1786871600300)},
}

// renameSession retargets a transcript copy at another id and cwd so two
// fixtures can live in different munged directories.
func renameSession(body, id, cwd string) string {
	s := strings.Replace(body, "session-abc", id, 1)
	return strings.Replace(s, "/home/u/src/xurl", cwd, 1)
}

func writeSession(t *testing.T, root, munged, id, body string, compressed bool, mod time.Time) {
	t.Helper()
	dir := filepath.Join(root, "sessions", munged, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "session.jsonl"
	var buf []byte
	if compressed {
		name = "session.jsonl.zstd"
		var z bytes.Buffer
		w, err := zstd.NewWriter(&z)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		buf = z.Bytes()
	} else {
		buf = []byte(body)
	}
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func checkThread(t *testing.T, t2 session.Thread) {
	t.Helper()
	if t2.Source.Ref.Provider != session.ProviderDeepSeek || t2.Source.Ref.SessionID != "session-abc" {
		t.Fatalf("source ref = %+v", t2.Source.Ref)
	}
	md := t2.Source.Metadata
	if md["title"] != "dsh support" || md["cwd"] != "/home/u/src/xurl" {
		t.Fatalf("metadata = %+v", md)
	}
	if md["model"] != "deepseek-v4-flash" || md["model_provider"] != "deepseek-official" {
		t.Fatalf("model metadata = %+v", md)
	}
	if len(t2.Entries) != len(wantEntries) {
		t.Fatalf("got %d entries: %+v", len(t2.Entries), t2.Entries)
	}
	for i, e := range t2.Entries {
		if e != wantEntries[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, e, wantEntries[i])
		}
	}
}

func TestReadDeepSeekSession(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "--home-u-src-xurl--", "session-abc", transcript, false, time.Now())

	p := New()
	src, err := p.Resolve(context.Background(), session.Roots{DeepSeek: root}, "session-abc")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := p.Read(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	checkThread(t, thread)
}

func TestReadDeepSeekSessionZstd(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "--home-u-src-xurl--", "session-abc", transcript, true, time.Now())

	p := New()
	thread, err := p.Read(context.Background(), session.Source{
		Ref:  session.Ref{Provider: session.ProviderDeepSeek, SessionID: "session-abc"},
		Path: filepath.Join(root, "sessions", "--home-u-src-xurl--", "session-abc", "session.jsonl.zstd"),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkThread(t, thread)
}

func TestReadTornTail(t *testing.T) {
	root := t.TempDir()
	// A crashed writer's last line is half-written; the parsed prefix must
	// survive with a warning.
	torn := transcript + `{"type":"turn/sta`
	writeSession(t, root, "--home-u-src-xurl--", "session-abc", torn, false, time.Now())

	p := New()
	thread, err := p.Read(context.Background(), session.Source{
		Ref:  session.Ref{Provider: session.ProviderDeepSeek, SessionID: "session-abc"},
		Path: filepath.Join(root, "sessions", "--home-u-src-xurl--", "session-abc", "session.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Warnings) != 1 || !strings.Contains(thread.Warnings[0], "malformed record") {
		t.Fatalf("warnings = %+v", thread.Warnings)
	}
	if len(thread.Entries) != len(wantEntries) {
		t.Fatalf("got %d entries, want the full parsed prefix: %+v", len(thread.Entries), thread.Entries)
	}
}

func TestResolveNewestAndByID(t *testing.T) {
	root := t.TempDir()
	old := renameSession(transcript, "session-old", "/home/u/src/other")
	writeSession(t, root, "--home-u-src-other--", "session-old", old, false, time.Now().Add(-2*time.Hour))
	writeSession(t, root, "--home-u-src-xurl--", "session-abc", transcript, false, time.Now())

	p := New()
	src, err := p.Resolve(context.Background(), session.Roots{DeepSeek: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "session-abc" {
		t.Fatalf("newest = %q, want session-abc", src.Ref.SessionID)
	}

	src, err = p.Resolve(context.Background(), session.Roots{DeepSeek: root}, "session-old")
	if err != nil {
		t.Fatal(err)
	}
	if src.Ref.SessionID != "session-old" {
		t.Fatalf("by id = %q, want session-old", src.Ref.SessionID)
	}

	if _, err := p.Resolve(context.Background(), session.Roots{DeepSeek: root}, "session-nope"); err == nil {
		t.Fatal("unknown id resolved without error")
	}
}

func TestListFilters(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "--home-u-src-xurl--", "session-abc", transcript, false, time.Now())
	writeSession(t, root, "--home-u-src-other--", "session-old", renameSession(transcript, "session-old", "/home/u/src/other"), false, time.Now().Add(-2*time.Hour))

	p := New()
	sums, err := p.List(context.Background(), session.Roots{DeepSeek: root}, session.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 || sums[0].Ref.SessionID != "session-abc" || sums[0].Rank != 1 || sums[1].Rank != 2 {
		t.Fatalf("listing = %+v", sums)
	}
	if sums[0].Title != "dsh support" || sums[0].Cwd != "/home/u/src/xurl" || sums[0].Preview != "support dsh" {
		t.Fatalf("summary[0] = %+v", sums[0])
	}

	sums, err = p.List(context.Background(), session.Roots{DeepSeek: root}, session.ListOptions{Cwd: "/home/u/src/other"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Ref.SessionID != "session-old" {
		t.Fatalf("cwd-filtered listing = %+v", sums)
	}

	sums, err = p.List(context.Background(), session.Roots{DeepSeek: root}, session.ListOptions{Query: "inspect"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 2 {
		t.Fatalf("query listing = %+v", sums)
	}

	sums, err = p.List(context.Background(), session.Roots{DeepSeek: root}, session.ListOptions{Query: "no-such-word"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 0 {
		t.Fatalf("query listing = %+v, want none", sums)
	}
}

func TestEmptyRoot(t *testing.T) {
	p := New()
	if _, err := p.Resolve(context.Background(), session.Roots{DeepSeek: t.TempDir()}, ""); err == nil {
		t.Fatal("resolve on empty root succeeded")
	}
	sums, err := p.List(context.Background(), session.Roots{DeepSeek: t.TempDir()}, session.ListOptions{})
	if err != nil || len(sums) != 0 {
		t.Fatalf("list on empty root = %+v, %v", sums, err)
	}
}

func TestTitleFallsBackToCwdBase(t *testing.T) {
	root := t.TempDir()
	// Drop the session/title line: the fallback is the cwd's base name.
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(transcript), "\n") {
		if strings.Contains(l, `"type":"session/title"`) {
			continue
		}
		lines = append(lines, l)
	}
	writeSession(t, root, "--home-u-src-xurl--", "session-abc", strings.Join(lines, "\n")+"\n", false, time.Now())

	p := New()
	src, err := p.Resolve(context.Background(), session.Roots{DeepSeek: root}, "")
	if err != nil {
		t.Fatal(err)
	}
	if src.Metadata["title"] != "xurl" {
		t.Fatalf("title = %q, want xurl", src.Metadata["title"])
	}
}
