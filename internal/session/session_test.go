package session

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFailurePreservesStructuredInput(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`{"command": "go test ./..."}`, `{"command":"go test ./..."}`},
		{`{"file_path": "/a/b.go", "old_string": "x"}`, `{"file_path":"/a/b.go","old_string":"x"}`},
		{`{"count": 3}`, `{"count":3}`},
		{`"plain"`, `"plain"`},
		{``, ""},
		{`null`, ""},
		{`{"cmd": "x"`, `"{\"cmd\": \"x\""`},
	}
	for _, c := range cases {
		e := Failure("tool", json.RawMessage(c.raw), "failed", time.Time{})
		if e.Input != c.want {
			t.Errorf("Failure input %s = %q, want %q", c.raw, e.Input, c.want)
		}
	}
}

// Text output keeps object keys and argv intact so a receiving agent can
// reconstruct the failed call. A scalar JSON string needs no JSON quotes.
func TestInputTextPreservesCallShape(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`{"command": "go test ./..."}`, `{"command":"go test ./..."}`},
		{`{"url": "https://example.com"}`, `{"url":"https://example.com"}`},
		{`["/bin/zsh","-lc","go test ./..."]`, `["/bin/zsh","-lc","go test ./..."]`},
		{`["python","-c","print('x')"]`, `["python","-c","print('x')"]`},
		{`["git","status"]`, `["git","status"]`},
		{`{"file_path": "/a/b.go", "old_string": "x"}`, `{"file_path":"/a/b.go","old_string":"x"}`},
		{`{"count": 3}`, `{"count":3}`},
		{`"plain"`, "plain"},
		{``, ""},
	}
	for _, c := range cases {
		e := Failure("tool", json.RawMessage(c.raw), "failed", time.Time{})
		if got := e.InputText(); got != c.want {
			t.Errorf("InputText(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestFailurePairsKindAndRole(t *testing.T) {
	at := time.Date(2026, 9, 2, 4, 11, 45, 0, time.UTC)
	e := Failure("Bash", json.RawMessage(`{"command":"go test ./..."}`), "FAIL", at)
	want := Entry{Kind: KindFailure, Role: RoleTool, Tool: "Bash", Input: `{"command":"go test ./..."}`, Text: "FAIL", Time: at}
	if e != want {
		t.Fatalf("Failure() = %+v, want %+v", e, want)
	}
}

func TestPreviewSkipsFailures(t *testing.T) {
	th := Thread{Entries: []Entry{
		Failure("Bash", json.RawMessage(`{"command":"ls"}`), "no such file", time.Time{}),
		{Kind: KindMessage, Role: RoleUser, Text: "hello"},
	}}
	if got := th.Preview(); got != "hello" {
		t.Fatalf("Preview() = %q, want %q", got, "hello")
	}
}
