package session

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// mayMatch copies s so a test can reuse a literal that MayMatch lowercases.
func mayMatchStr(s, query, cwd string) bool {
	return ListOptions{Query: query, Cwd: cwd}.MayMatch([]byte(s))
}

func TestMayMatchQuery(t *testing.T) {
	const file = `{"type":"message","text":"Deploying the Egress fix"}`
	tests := []struct {
		name  string
		raw   string
		query string
		want  bool
	}{
		{"present", file, "egress", true},
		{"absent", file, "kubernetes", false},
		{"query upper cased", file, "EGRESS", true},
		{"file upper cased", file, "deploying", true},
		{"empty query passes", file, "", true},
		{"substring across words", file, "the egress", true},
		{"no filter at all", file, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mayMatchStr(tc.raw, tc.query, ""); got != tc.want {
				t.Errorf("MayMatch(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

// A query a JSON writer may have escaped must never be filtered out, because the
// file holds the escape and not the byte the caller asked for.
func TestMayMatchUnprefilterableQueries(t *testing.T) {
	const file = `{"text":"nothing relevant here"}`
	for _, q := range []string{
		`say "hello"`, // a quote is always escaped
		`C:\Users`,    // a backslash is always escaped
		"two\nlines",  // control bytes are always escaped
		"tab\there",   // ...including tab
		"café",        // cased non-ASCII needs Unicode folding
		"Привет",      // ...in any script
	} {
		if !mayMatchStr(file, q, "") {
			t.Errorf("MayMatch(%q) = false, want true: an unprefilterable query must fall through to the parser", q)
		}
	}
}

// A file that escapes one of a needle's own characters cannot be decided from
// its bytes; a file that escapes something else still can. The rule reads what
// the file actually escaped, not what its writer is assumed to escape.
func TestMayMatchWriterEscapedBytes(t *testing.T) {
	for _, c := range strings.Split(`<>&'/`, "") {
		q := "a" + c + "b"
		if mayMatchStr(`{"text":"nothing relevant"}`, q, "") {
			t.Errorf("MayMatch(%q) on an escape-free file = true, want false: it is absent and prefilterable", q)
		}
		hiding := `{"text":"nothing ` + esc(fmt.Sprintf("%04x", c[0])) + ` relevant"}`
		if !mayMatchStr(hiding, q, "") {
			t.Errorf("MayMatch(%q) on a file that escapes %q = false, want true", q, c)
		}
		if mayMatchStr(`{"text":"caf`+esc("00e9")+`"}`, q, "") {
			t.Errorf("MayMatch(%q) on a file escaping only an accent = true, want false: that escape cannot hide it", q)
		}
		if !mayMatchStr(`{"text":"a`+c+`b"}`, q, "") {
			t.Errorf("MayMatch(%q) = false, want true: it is present verbatim", q)
		}
	}
	// A slash query is also unsafe against a writer that emits \/.
	if !mayMatchStr(`{"text":"http:\/\/example.com"}`, "src/catchup", "") {
		t.Error(`MayMatch on a file using \/ = false, want true`)
	}
}

// Caseless non-ASCII compares bytewise, which is what keeps CJK queries fast.
func TestMayMatchCaselessNonASCII(t *testing.T) {
	const file = `{"text":"这是一个搜索方案"}`
	if !mayMatchStr(file, "搜索", "") {
		t.Error("MayMatch(搜索) = false, want true")
	}
	if mayMatchStr(file, "部署", "") {
		t.Error("MayMatch(部署) = true, want false: absent from an escape-free file")
	}
	if !mayMatchStr(`{"text":"\u641c\u7d22"}`, "搜索", "") {
		t.Error(`MayMatch(搜索) on a \u-escaped file = false, want true`)
	}
}

func TestMayMatchCwd(t *testing.T) {
	const file = `{"type":"session_meta","payload":{"cwd":"/home/u/src/catchup"}}`
	if !mayMatchStr(file, "", "/home/u/src/catchup") {
		t.Error("exact cwd = false, want true")
	}
	if mayMatchStr(file, "", "/home/u/src/otherproject") {
		t.Error("unrelated cwd = true, want false")
	}
	// Every spelling that Clean-collapses to the recorded directory must survive.
	for _, stored := range []string{
		`{"cwd":"/home/u/src/catchup/"}`,
		`{"cwd":"/home/u/src/./catchup"}`,
		`{"cwd":"/home/u/src/attic/../catchup"}`,
		`{"cwd":"/home//u/src/catchup"}`,
	} {
		if !mayMatchStr(stored, "", "/home/u/src/catchup") {
			t.Errorf("stored %s = false, want true: it Cleans to the requested directory", stored)
		}
	}
	if !mayMatchStr(file, "", "/HOME/U/SRC/CATCHUP") {
		t.Error("cwd differing only in case = false, want true")
	}
}

// The two conditions are independent: failing either one is enough to skip.
func TestMayMatchBothConditions(t *testing.T) {
	const file = `{"cwd":"/home/u/src/catchup","text":"the egress fix"}`
	if !mayMatchStr(file, "egress", "/home/u/src/catchup") {
		t.Error("both present = false, want true")
	}
	if mayMatchStr(file, "egress", "/home/u/src/other") {
		t.Error("query present, cwd absent = true, want false")
	}
	if mayMatchStr(file, "kubernetes", "/home/u/src/catchup") {
		t.Error("cwd present, query absent = true, want false")
	}
}

func TestMayMatchEmptyInputs(t *testing.T) {
	if !mayMatchStr("", "", "") {
		t.Error("no filter = false, want true")
	}
	if mayMatchStr("", "egress", "") {
		t.Error("empty file with a query = true, want false")
	}
	if !(ListOptions{}).MayMatch(nil) {
		t.Error("nil raw with no filter = false, want true")
	}
}

// MayMatch must never contradict the filter the providers apply after parsing.
func TestMayMatchAgreesWithConfirmedFilter(t *testing.T) {
	visible := "Deploying the Egress fix\nsecond turn"
	raw := `{"text":"Deploying the Egress fix"}` + "\n" + `{"text":"second turn"}`
	for _, q := range []string{"egress", "EGRESS", "second", "kubernetes", "deploying the"} {
		confirmed := strings.Contains(strings.ToLower(visible), strings.ToLower(q))
		if confirmed && !mayMatchStr(raw, q, "") {
			t.Errorf("MayMatch(%q) = false but the parsed filter matches: a session would go missing", q)
		}
	}
}

func TestSameDirAgreesWithCwdPrefilter(t *testing.T) {
	want := filepath.Clean("/home/u/src/catchup")
	for _, stored := range []string{
		"/home/u/src/catchup",
		"/home/u/src/catchup/",
		"/home/u/src/./catchup",
	} {
		if !SameDir(stored, want) {
			t.Fatalf("test premise broken: SameDir(%q, %q) = false", stored, want)
		}
		if !mayMatchStr(`{"cwd":"`+stored+`"}`, "", want) {
			t.Errorf("SameDir accepts %q but the prefilter rejects it", stored)
		}
	}
}

// containsFold folds the needle itself, so no caller can turn a mixed-case
// query into a confident miss - the one answer the prefilter must never give.
func TestMayMatchUnfoldedNeedle(t *testing.T) {
	const file = `{"text":"deploying the egress fix"}`
	for _, q := range []string{"Egress", "EGRESS", "eGrEsS", "egress"} {
		if !mayMatchStr(file, q, "") {
			t.Errorf("query %q = false, want true", q)
		}
	}
	for _, dir := range []string{"/Home/Wilbeibi/Src", "/home/wilbeibi/src"} {
		if !mayMatchStr(`{"cwd":"/home/wilbeibi/src"}`, "", dir) {
			t.Errorf("cwd %q = false, want true", dir)
		}
	}
}

// esc builds a JSON escape sequence at runtime. Written this way because a
// literal backslash in a fixture is easy to lose to one layer of quoting or
// another, and a fixture that quietly holds the decoded form tests nothing:
// the first version of this test passed against bytes that contained no escape.
func esc(hex string) string { return string(rune(92)) + "u" + hex }

// A recorded directory reaches the file inside a JSON string, so it can arrive
// escaped: Go's encoder escapes & < > as six-byte \uXXXX, and an ASCII-safe
// encoder does the same to every non-ASCII rune. The prefilter cannot find those
// bytes, so it must defer to the parser rather than report a miss.
func TestMayMatchEscapedCwd(t *testing.T) {
	cases := []struct {
		name, raw, cwd string
	}{
		{"ampersand in final element", `{"cwd":"/tmp/a` + esc("0026") + `b"}`, "/tmp/a&b"},
		{"angle bracket in final element", `{"cwd":"/tmp/a` + esc("003c") + `b"}`, "/tmp/a<b"},
		{"non-ascii final element", `{"cwd":"/home/w/` + esc("9879") + esc("76ee") + `"}`, "/home/w/项目"},
		{"escaped parent, plain final element", `{"cwd":"/tmp/a` + esc("0026") + `b/proj"}`, "/tmp/a&b/proj"},
	}
	for _, c := range cases {
		if !mayMatchStr(c.raw, "", c.cwd) {
			t.Errorf("%s: MayMatch(%q, cwd=%q) = false, want true: the parser decodes this to a match", c.name, c.raw, c.cwd)
		}
	}
}

// A separator reaches the file escaped two ways and only one of them leaves the
// byte behind: \/ still contains it, so the final element stays findable and the
// prefilter keeps deciding; \u002f does not, so it must defer.
func TestMayMatchEscapedSeparator(t *testing.T) {
	b := string(rune(92))
	raw := `{"cwd":"` + b + `/home` + b + `/w` + b + `/src"}`
	if !mayMatchStr(raw, "", "/home/w/src") {
		t.Errorf("MayMatch(%q) = false, want true", raw)
	}
	if mayMatchStr(raw, "", "/home/w/other") {
		t.Error("MayMatch accepted an unrelated directory; the prefilter stopped deciding")
	}
	for _, c := range []struct{ name, raw, cwd string }{
		{"escaped /", `{"cwd":"` + esc("002f") + `home` + esc("002f") + `w` + esc("002f") + `src"}`, "/home/w/src"},
		{"escaped \\", `{"cwd":"` + esc("005c") + `w` + esc("005c") + `src"}`, "/w/src"},
	} {
		if !mayMatchStr(c.raw, "", c.cwd) {
			t.Errorf("%s: MayMatch(%q, cwd=%q) = false, want true: the escape erased the separator the probes search for",
				c.name, c.raw, c.cwd)
		}
	}
}

// JSON permits \uXXXX for any character, so an escape can hide any needle - but
// only a needle whose own characters the file escaped. Deciding by the presence
// of an escape rather than its content would be sound and nearly inert: a third
// of this corpus's files carry one, and they hold 85% of its bytes.
func TestMayMatchEscapePrecision(t *testing.T) {
	if !mayMatchStr(`{"text":"`+esc("0064")+`eploy"}`, "deploy", "") {
		t.Error("MayMatch rejected a file whose escape decodes to the query")
	}
	if mayMatchStr(`{"text":"caf`+esc("00e9")+`"}`, "deploy", "") {
		t.Error("MayMatch deferred on an escape that cannot hide an ASCII query")
	}
	if mayMatchStr(`{"text":"nothing relevant here"}`, "deploy", "") {
		t.Error("MayMatch stopped deciding on an escape-free file; the prefilter is inert")
	}
	// Any non-ASCII escape can hide a non-ASCII needle.
	if !mayMatchStr(`{"text":"caf`+esc("00e9")+`"}`, "项目", "") {
		t.Error("MayMatch decided a miss for a CJK query against a file with non-ASCII escapes")
	}
}
