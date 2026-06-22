// Package store ties sources to the graph: it runs the ingest loop, swaps each
// source's edges on refresh, re-materializes the all-pairs snapshot, and serves
// reads from that immutable snapshot under an RWMutex (O(1), no base traversal).
package store

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/vul-os/openrate/internal/graph"
	"github.com/vul-os/openrate/internal/sources"
)

// SourceStatus records the outcome of the last fetch for one source.
type SourceStatus struct {
	Name      string    `json:"name"`
	Edges     int       `json:"edges"`
	LastOK    time.Time `json:"last_ok"`
	LastError string    `json:"last_error,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	g        *graph.Graph
	snap     *graph.Snapshot
	srcs     []sources.Source
	status   map[string]*SourceStatus
	interval time.Duration
}

func New(interval time.Duration, srcs ...sources.Source) *Store {
	st := &Store{
		g:        graph.New(),
		srcs:     srcs,
		status:   map[string]*SourceStatus{},
		interval: interval,
	}
	for _, s := range srcs {
		st.status[s.Name()] = &SourceStatus{Name: s.Name()}
	}
	st.snap = st.g.Materialize(time.Now().UTC())
	return st
}

// Snapshot returns the current immutable all-pairs view.
func (s *Store) Snapshot() *graph.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// Status returns a copy of per-source fetch status.
func (s *Store) Status() []SourceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SourceStatus, 0, len(s.status))
	for _, s := range s.status {
		out = append(out, *s)
	}
	return out
}

// refresh fetches every source concurrently, replaces their edges, and rebuilds
// the snapshot once. A failing source keeps its previous edges.
func (s *Store) refresh(ctx context.Context) {
	type result struct {
		name  string
		edges []graph.Edge
		err   error
	}
	results := make(chan result, len(s.srcs))
	for _, src := range s.srcs {
		go func(src sources.Source) {
			c, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			edges, err := src.Fetch(c)
			results <- result{src.Name(), edges, err}
		}(src)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for range s.srcs {
		r := <-results
		st := s.status[r.name]
		if r.err != nil {
			st.LastError = r.err.Error()
			log.Printf("source %s: %v", r.name, r.err)
			continue
		}
		st.LastError = ""
		st.LastOK = now
		st.Edges = len(r.edges)
		s.g.Replace(r.name, r.edges)
	}
	s.snap = s.g.Materialize(now)
}

// Run does an immediate refresh, then refreshes on the configured interval until
// ctx is cancelled. Designed to be pushed toward streaming later (see CLOUD.md).
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
