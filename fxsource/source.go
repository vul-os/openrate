// Package fxsource defines pluggable rate providers and the adapters openrate
// ships for them. Each Source ingests "the open way" — central-bank reference
// files and free public venue feeds — and emits native-base edges into the
// graph. No source is privileged as the base.
//
// Constructing a Source opens nothing and reads nothing: an adapter touches the
// network only inside Fetch, and reads its API key (if it needs one) at that
// moment. Build resolves a name list into adapters and is pure; FromEnv is the
// one call in the whole module that consults the environment.
package fxsource

import (
	"context"
	"time"

	"github.com/vul-os/openrate/fx"
)

// Source fetches a fresh set of edges. Name must be stable: the graph keys a
// source's edges by it so a refresh replaces them atomically.
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]fx.Edge, error)
}

// Status records the outcome of the last fetch for one source. A Refresher
// reports one of these per configured source, and the JSON shape is the one
// /api/v1/meta has always published.
type Status struct {
	Name      string    `json:"name"`
	Edges     int       `json:"edges"`
	LastOK    time.Time `json:"last_ok"`
	LastError string    `json:"last_error,omitempty"`
}
