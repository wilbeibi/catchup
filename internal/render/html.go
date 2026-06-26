package render

import (
	"html/template"
	"io"

	"github.com/wilbeibi/baton/internal/session"
)

// htmlThread renders a self-contained HTML document with inline CSS and no
// JavaScript. All interpolated text is escaped by html/template, so escaping is
// guaranteed by construction rather than by hand.
func htmlThread(w io.Writer, t session.Thread) error {
	return htmlTmpl.Execute(w, htmlModel{
		Title:    docTitle(t.Source),
		Header:   header(t.Source),
		Warnings: allWarnings(t),
		Entries:  htmlEntries(t.Entries),
	})
}

// htmlMeta renders a Source's metadata as a standalone HTML document.
func htmlMeta(w io.Writer, s session.Source) error {
	return htmlTmpl.Execute(w, htmlModel{
		Title:    docTitle(s),
		Header:   header(s),
		Warnings: s.Warnings,
	})
}

type htmlModel struct {
	Title    string
	Header   []kv
	Warnings []string
	Entries  []htmlEntry
}

type htmlEntry struct {
	Index int
	Label string
	Role  string
	Time  string
	Text  string
}

func htmlEntries(entries []session.Entry) []htmlEntry {
	out := make([]htmlEntry, len(entries))
	for i, e := range entries {
		ts := ""
		if !e.Time.IsZero() {
			ts = e.Time.UTC().Format(tsHuman)
		}
		out[i] = htmlEntry{
			Index: i + 1,
			Label: entryLabel(e),
			Role:  e.Role,
			Time:  ts,
			Text:  e.Text,
		}
	}
	return out
}

func docTitle(s session.Source) string {
	if t := s.Metadata["title"]; t != "" {
		return t
	}
	if s.Ref.SessionID != "" {
		return s.Ref.Provider + " · " + s.Ref.SessionID
	}
	return s.Ref.Provider + " session"
}

var htmlTmpl = template.Must(template.New("thread").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root { color-scheme: light dark; }
body { font: 16px/1.6 system-ui, sans-serif; max-width: 48rem; margin: 2rem auto; padding: 0 1rem; }
h1 { font-size: 1.4rem; margin: 0 0 1rem; }
dl.meta { display: grid; grid-template-columns: max-content 1fr; gap: .15rem .75rem; margin: 0 0 2rem; font-size: .85rem; opacity: .85; }
dl.meta dt { font-weight: 600; }
dl.meta dd { margin: 0; overflow-wrap: anywhere; }
.warnings { border-left: 3px solid #c93; padding: .25rem .75rem; margin: 0 0 2rem; font-size: .85rem; }
section.entry { margin: 0 0 1.5rem; }
section.entry h2 { font-size: .8rem; text-transform: uppercase; letter-spacing: .04em; opacity: .6; margin: 0 0 .35rem; }
section.entry.role-user h2 { color: #2a6; }
section.entry.role-assistant h2 { color: #46c; }
section.entry.kind-compact h2 { color: #c93; }
pre { white-space: pre-wrap; overflow-wrap: anywhere; margin: 0; font: inherit; }
</style>
</head>
<body>
<header>
<h1>{{.Title}}</h1>
<dl class="meta">
{{- range .Header}}
<dt>{{.Key}}</dt><dd>{{.Val}}</dd>
{{- end}}
</dl>
{{- if .Warnings}}
<div class="warnings">{{range .Warnings}}<div>⚠ {{.}}</div>{{end}}</div>
{{- end}}
</header>
{{- range .Entries}}
<section class="entry role-{{.Role}} kind-{{.Label}}">
<h2>{{.Index}}. {{.Label}}{{if .Time}} · {{.Time}}{{end}}</h2>
<pre>{{.Text}}</pre>
</section>
{{- end}}
</body>
</html>
`))
