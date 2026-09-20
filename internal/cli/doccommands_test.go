package cli

import (
	"fmt"
	"strings"
	"testing"
)

// SKILL.md is the grammar an agent reads before it ever runs catchup, and it
// is the copy that fails quietly: a person who hits a rejected command tries
// another spelling, while an agent following a stale line concludes catchup
// cannot do the thing and stops. So every documented `catchup …` line in it
// goes through Parse — pure syntax, which reads no disk, launches nothing, and
// never exits. README.md and the help screen are a reader's to correct.

// descriptionLimit is the character budget installers enforce, usually silently.
const descriptionLimit = 1024

// placeholders maps every <…> slot the documents use to a concrete value, so a
// shape is checked rather than its wording. An unmapped slot fails the run: as
// a literal, <thing> would read as an agent name and quietly pass.
var placeholders = map[string]string{
	"<agent>": "codex",
	"<other>": "claude",
	"<id>":    "019f-abc",
}

// docCommand is one line of command text: where, as written, and its argv.
type docCommand struct {
	line int
	raw  string
	args []string
}

// readDocLF normalizes line endings: a CRLF checkout — the default on Windows,
// where CI also runs — would glue a "\r" to every command's last argument.
func readDocLF(t *testing.T, name string) string {
	t.Helper()
	return strings.ReplaceAll(readDoc(t, name), "\r\n", "\n")
}

// markdownCode returns every line inside a fenced block, plus every backtick
// span — where a bare `catchup fork` mention of a removed verb would hide.
func markdownCode(md string) []docCommand {
	var frags []docCommand
	fenced := false
	for i, line := range strings.Split(md, "\n") {
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "```"):
			fenced = !fenced
		case fenced:
			frags = append(frags, docCommand{line: i + 1, raw: line})
		default:
			for _, span := range inlineSpans(line) {
				frags = append(frags, docCommand{line: i + 1, raw: span})
			}
		}
	}
	return frags
}

// inlineSpans returns the contents of each backtick pair on one line. An
// unterminated backtick ends the scan: it is prose punctuation, not a span.
func inlineSpans(line string) []string {
	var spans []string
	for {
		_, rest, ok := strings.Cut(line, "`")
		if !ok {
			return spans
		}
		var span string
		if span, line, ok = strings.Cut(rest, "`"); !ok {
			return spans
		}
		spans = append(spans, span)
	}
}

// extractCommands turns code text into invocations Parse can be given. A segment
// counts when its first field is `catchup`, which leaves wilbeibi/tap/catchup and
// `.catchup/` alone; the helpers below handle what a shell strips before argv.
func extractCommands(frags []docCommand) ([]docCommand, error) {
	var cmds []docCommand
	for _, f := range frags {
		for _, seg := range segments(strings.Fields(f.raw)) {
			if len(seg) == 0 || seg[0] != "catchup" {
				continue
			}
			args := make([]string, 0, len(seg)-1)
			for _, field := range seg[1:] {
				v, err := fillPlaceholders(strings.Trim(field, `"'`))
				if err != nil {
					return nil, fmt.Errorf("line %d: %s: %w", f.line, strings.TrimSpace(f.raw), err)
				}
				args = append(args, v)
			}
			cmds = append(cmds, docCommand{f.line, strings.TrimSpace(f.raw), args})
		}
	}
	return cmds, nil
}

// segments splits fields into shell commands, cutting each at a # comment or a
// > redirection: neither reaches argv.
func segments(fields []string) [][]string {
	var out [][]string
	var cur []string
	cut := false
	for _, f := range fields {
		switch {
		case f == "|" || f == "||" || f == "&&" || f == ";":
			out, cur, cut = append(out, cur), nil, false
		case strings.HasPrefix(f, "#"), strings.HasPrefix(f, ">"):
			cut = true
		case !cut:
			cur = append(cur, f)
		}
	}
	return append(out, cur)
}

// fillPlaceholders substitutes each <…> slot inside one field, so that
// <agent>/3 is checked as a real target rather than as a literal name.
func fillPlaceholders(field string) (string, error) {
	var b strings.Builder
	for {
		i, j := strings.IndexByte(field, '<'), strings.IndexByte(field, '>')
		if i < 0 || j < i {
			b.WriteString(field)
			return b.String(), nil
		}
		v, ok := placeholders[field[i:j+1]]
		if !ok {
			return "", fmt.Errorf("unknown placeholder %q; add it to placeholders", field[i:j+1])
		}
		b.WriteString(field[:i])
		b.WriteString(v)
		field = field[j+1:]
	}
}

// parseFailures names the document line, the command as written, and the error.
func parseFailures(doc string, cmds []docCommand) []string {
	var bad []string
	for _, c := range cmds {
		if _, err := Parse(c.args); err != nil {
			bad = append(bad, fmt.Sprintf("%s:%d: %s\n    parsed as %q: %v", doc, c.line, c.raw, c.args, err))
		}
	}
	return bad
}

func TestDocumentedCommandsParse(t *testing.T) {
	docs := []struct {
		name    string
		frags   []docCommand
		atLeast int
	}{
		{"SKILL.md", markdownCode(readDocLF(t, "SKILL.md")), 12},
	}
	for _, d := range docs {
		t.Run(d.name, func(t *testing.T) {
			cmds, err := extractCommands(d.frags)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: %d catchup invocations", d.name, len(cmds))
			for _, c := range cmds {
				t.Logf("  %s:%d  catchup %s", d.name, c.line, strings.Join(c.args, " "))
			}
			if len(cmds) < d.atLeast {
				t.Fatalf("extracted %d commands, want at least %d; the extractor is broken", len(cmds), d.atLeast)
			}
			for _, msg := range parseFailures(d.name, cmds) {
				t.Errorf("documented command the binary rejects:\n%s", msg)
			}
		})
	}
}

// TestSkillFrontmatter gates the half of SKILL.md that is not a command: an
// over-long description, or one carrying <…> tokens a validator reads as
// markup, is rejected at install time with nothing said.
func TestSkillFrontmatter(t *testing.T) {
	body, _, ok := splitFrontmatter([]byte(readDocLF(t, "SKILL.md")))
	if !ok {
		t.Fatal("SKILL.md: no YAML frontmatter block")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		if k, v, found := strings.Cut(line, ":"); found {
			fields[k] = strings.TrimSpace(v)
		}
	}
	// "catchup" is the directory installSkill writes each copy into, so it is
	// also the name a harness looks the skill up by.
	if got := fields["name"]; got != "catchup" {
		t.Errorf("SKILL.md frontmatter name = %q, want %q", got, "catchup")
	}
	desc := fields["description"]
	if desc == "" {
		t.Fatal("SKILL.md frontmatter has no description")
	}
	n := len([]rune(desc))
	t.Logf("SKILL.md description: %d characters, %d of headroom under %d", n, descriptionLimit-n, descriptionLimit)
	if n > descriptionLimit {
		t.Errorf("SKILL.md description is %d characters, over the %d limit by %d", n, descriptionLimit, n-descriptionLimit)
	}
	if i := strings.IndexByte(desc, '<'); i >= 0 {
		t.Errorf("SKILL.md description carries a <…> token at offset %d; a validator reading it as markup drops it", i)
	}
}

// The extractor can fail silently — a scanner that finds nothing passes every
// check above — so it is pinned to synthetic documents, not the real files.
func TestExtractCommands(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		want    []string // "<line>:<args>" per extracted command
		failure string   // the sole Parse rejection, "" when every command parses
		err     string   // the sole extraction error, "" when there is none
	}{
		{
			name: "fences, spans, comments, redirections and chains",
			doc: "# Doc\n" +
				"Prose naming `catchup` bare and `wilbeibi/tap/catchup`, which is not an invocation.\n" +
				"```bash\n" +
				"catchup <agent> --last 20      # trailing comment\n" +
				"catchup <agent>/3 -q \"topic\"\n" +
				"catchup fork <agent> --into <other>\n" +
				"catchup <agent> --html > page.html\n" +
				"cd /tmp && catchup --list | rg session\n" +
				"brew install wilbeibi/tap/catchup\n" +
				"```\n" +
				"Back in prose: `catchup <agent> --id <id> --agent`.\n",
			want: []string{"2:", "4:codex --last 20", "5:codex/3 -q topic", "6:fork codex --into claude",
				"7:codex --html", "8:--list", "11:codex --id 019f-abc --agent"},
		},
		{
			name:    "a flag the binary lacks is reported with its line",
			doc:     "```bash\ncatchup <agent> --bogus\n```\n",
			want:    []string{"2:codex --bogus"},
			failure: "SCRATCH.md:2: catchup <agent> --bogus\n    parsed as [\"codex\" \"--bogus\"]: unknown flag \"--bogus\"",
		},
		{
			name: "an unmapped placeholder stops the run",
			doc:  "```bash\ncatchup <thing> --list\n```\n",
			err:  `line 2: catchup <thing> --list: unknown placeholder "<thing>"; add it to placeholders`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmds, err := extractCommands(markdownCode(tt.doc))
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("error = %v, want %v", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, c := range cmds {
				got = append(got, fmt.Sprintf("%d:%s", c.line, strings.Join(c.args, " ")))
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("extracted\n  %v\nwant\n  %v", got, tt.want)
			}
			if bad := strings.Join(parseFailures("SCRATCH.md", cmds), "\n"); bad != tt.failure {
				t.Errorf("failures =\n%s\nwant\n%s", bad, tt.failure)
			}
		})
	}
}
