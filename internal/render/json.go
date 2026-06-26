package render

import (
	"fmt"
	"io"

	"github.com/wilbeibi/baton/internal/session"
)

// jsonThread encodes a Thread as JSON. The wire shape is a deliberate
// projection of the core types (not their raw struct tags) so the JSON contract
// can evolve independently of internal field names.
func jsonThread(w io.Writer, t session.Thread) error {
	return fmt.Errorf("render: jsonThread not implemented")
}

// jsonMeta encodes a Source's identity and metadata as JSON.
func jsonMeta(w io.Writer, s session.Source) error {
	return fmt.Errorf("render: jsonMeta not implemented")
}
