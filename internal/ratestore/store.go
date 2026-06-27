// Package ratestore ties interest-rate sources to the rates book: it runs the
// ingest loop, swaps each source's observations on refresh, re-materializes the
// all-series snapshot, and serves reads from that immutable snapshot under an
// RWMutex. It mirrors the FX store, but over rates.Book instead of graph.Graph.
package ratestore

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/vul-os/openrate/internal/rates"
	"github.com/vul-os/openrate/internal/ratesources"
	"github.com/vul-os/openrate/internal/redact"
)

// SourceStatus records the outcome of the last fetch for one source.
type SourceStatus struct {
	Name         string    `json:"name"`
	Observations int       `json:"observations"`
	LastOK       time.Time `json:"last_ok"`
	LastError    string    `json:"last_error,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	b        *rates.Book
	snap     *rates.Snapshot
	srcs     []ratesources.Source
	status   map[string]*SourceStatus
	interval time.Duration
}

func New(interval time.Duration, srcs ...ratesources.Source) *Store {
	st := &Store{
		b:        rates.New(),
		srcs:     srcs,
		status:   map[string]*SourceStatus{},
		interval: interval,
	}
	for _, s := range srcs {
		st.status[s.Name()] = &SourceStatus{Name: s.Name()}
	}
	st.snap = st.b.Materialize(time.Now().UTC())
	return st
}

// Snapshot returns the current immutable all-series view.
func (s *Store) Snapshot() *rates.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// Status returns a copy of per-source fetch status.
func (s *Store) Status() []SourceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SourceStatus, 0, len(s.status))
	for _, st := range s.status {
		out = append(out, *st)
	}
	return out
}

// refresh fetches every source concurrently, replaces its observations, and
// rebuilds the snapshot progressively as results arrive. A failing source keeps
// its previous observations. The lock is held only for the fast apply +
// materialize, never during network I/O.
func (s *Store) refresh(ctx context.Context) {
	type result struct {
		name string
		obs  []rates.Observation
		err  error
	}
	results := make(chan result, len(s.srcs))
	for _, src := range s.srcs {
		go func(src ratesources.Source) {
			c, cancel := context.WithTimeout(ctx, 50*time.Second)
			defer cancel()
			obs, err := src.Fetch(c)
			results <- result{src.Name(), obs, err}
		}(src)
	}

	for range s.srcs {
		r := <-results
		s.mu.Lock()
		now := time.Now().UTC()
		st := s.status[r.name]
		if r.err != nil {
			// Keyed APIs (e.g. FRED) embed their key in the request URL, which
			// net/http echoes inside *url.Error; redact before recording.
			safe := redact.Error(r.err)
			st.LastError = safe.Error()
			log.Printf("interest source %s: %v", r.name, safe)
		} else {
			st.LastError = ""
			st.LastOK = now
			st.Observations = len(r.obs)
			s.b.Replace(r.name, r.obs)
		}
		s.snap = s.b.Materialize(now)
		s.mu.Unlock()
	}
}

// Run does an immediate refresh, then refreshes on the configured interval until
// ctx is cancelled.
func (s *Store) Run(ctx context.Context) {
	s.refresh(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.refresh(ctx)
		}
	}
}
