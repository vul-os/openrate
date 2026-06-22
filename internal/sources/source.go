// Package sources defines pluggable rate providers. Each Source ingests "the
// open way" — central-bank reference files and free public venue feeds — and
// emits native-base edges into the graph. No source is privileged as the base.
package sources

import (
	"context"

	"github.com/vul-os/openrate/internal/graph"
)

// Source fetches a fresh set of edges. Name must be stable: the graph keys a
// source's edges by it so a refresh replaces them atomically.
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]graph.Edge, error)
}
