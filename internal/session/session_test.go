package session

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCallInput(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`{"command": "go test ./..."}`, "go test ./..."},
		{`{"file_path": "/a/b.go", "old_string": "x"}`, `{"file_path":"/a/b.go","old_string":"x"}`},
		{`{"count": 3}`, `{"count":3}`},
		{`"plain"`, `"plain"`},
		{``, ""},
		{`null`, ""},
		{`{"cmd": "x"`, `{"cmd": "x"`},
	}
	for _, c := range cases {
		if got := CallInput(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("CallInput(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestFailurePairsKindAndRole(t *testing.T) {
	at := time.Date(2026, 9, 2, 4, 11, 45, 0, time.UTC)
	e := Failure("Bash", "go test ./...", "FAIL", at)
	want := Entry{Kind: KindFailure, Role: RoleTool, Tool: "Bash", Input: "go test ./...", Text: "FAIL", Time: at}
	if e != want {
		t.Fatalf("Failure() = %+v, want %+v", e, want)
	}
}

func TestPreviewSkipsFailures(t *testing.T) {
	th := Thread{Entries: []Entry{
		Failure("Bash", "ls", "no such file", time.Time{}),
		{Kind: KindMessage, Role: RoleUser, Text: "hello"},
	}}
	if got := th.Preview(); got != "hello" {
		t.Fatalf("Preview() = %q, want %q", got, "hello")
	}
}
