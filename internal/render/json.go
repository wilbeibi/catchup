package render

import (
	"encoding/json"
	"io"
	"time"

	"github.com/wilbeibi/baton/internal/session"
)

// The JSON wire types are an explicit projection of the core types, not their
// raw struct tags, so the output contract is stable even if internal field
// names change. Times are RFC3339 strings; empty values are omitted.

type sourceDoc struct {
	Provider  string            `json:"provider"`
	SessionID string            `json:"session_id,omitempty"`
	Path      string            `json:"path,omitempty"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	Warnings  []string          `json:"warnings,omitempty"`
}

type entryDoc struct {
	Index int    `json:"index"`
	Kind  string `json:"kind"`
	Role  string `json:"role,omitempty"`
	Time  string `json:"time,omitempty"`
	Text  string `json:"text"`
}

type threadDoc struct {
	sourceDoc
	Entries []entryDoc `json:"entries"`
}

// jsonThread encodes a Thread as JSON.
func jsonThread(w io.Writer, t session.Thread) error {
	doc := threadDoc{
		sourceDoc: makeSourceDoc(t.Source),
		Entries:   make([]entryDoc, len(t.Entries)),
	}
	doc.Warnings = allWarnings(t)
	for i, e := range t.Entries {
		doc.Entries[i] = entryDoc{
			Index: i + 1,
			Kind:  e.Kind,
			Role:  e.Role,
			Time:  rfc3339(e.Time),
			Text:  e.Text,
		}
	}
	return encode(w, doc)
}

// jsonMeta encodes a Source's identity and metadata as JSON.
func jsonMeta(w io.Writer, s session.Source) error {
	return encode(w, makeSourceDoc(s))
}

func makeSourceDoc(s session.Source) sourceDoc {
	return sourceDoc{
		Provider:  s.Ref.Provider,
		SessionID: s.Ref.SessionID,
		Path:      s.Path,
		UpdatedAt: rfc3339(s.UpdatedAt),
		Metadata:  s.Metadata,
		Warnings:  s.Warnings,
	}
}

func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func encode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
