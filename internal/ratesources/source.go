// Package ratesources defines pluggable interest-rate providers. Each Source
// ingests "the open way" — central-bank and multilateral reference data, or a
// keyed commercial API — and emits canonical Observations. No source is
// privileged; the store reconciles them per series. This mirrors the FX
// sources package but yields rates.Observation rather than graph.Edge.
package ratesources

import (
	"context"

	"github.com/vul-os/openrate/internal/rates"
)

// Source fetches a fresh set of observations. Name must be stable: the book
// keys a source's observations by it so a refresh replaces them atomically.
type Source interface {
	Name() string
	Fetch(ctx context.Context) ([]rates.Observation, error)
}
