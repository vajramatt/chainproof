package adapters

import (
	"context"
	"github.com/vajramatt/chainproof/internal/proof"
)

// Adapter is the boundary between changing harness formats and the stable
// ChainProof event protocol. Provider-specific parsing never belongs in core.
type Adapter interface {
	Name() string
	Discover(context.Context) ([]Source, error)
	Collect(context.Context, Source, Cursor) (Batch, error)
}
type Source struct {
	ID       string         `json:"id"`
	Path     string         `json:"path,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}
type Cursor struct {
	Value string `json:"value"`
}
type Batch struct {
	Events   []proof.EventInput `json:"events"`
	Next     Cursor             `json:"next_cursor"`
	Complete bool               `json:"complete"`
}

// Collection cursors make repeated pulls idempotent. An adapter must preserve
// native event IDs and label all after-the-fact data as imported.
