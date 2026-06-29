package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// fakeSource is a sources.Source that returns a fixed set of edges instantly,
// allowing tests to drive the store without real network I/O.
type fakeSource struct {
	name  string
	edges []graph.Edge
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Fetch(_ context.Context) ([]graph.Edge, error) {
	return f.edges, nil
}

// TestSnapshotReadsDuringRefresh fires many concurrent Snapshot() calls while
// the store is continuously refreshing. Any data race on the RWMutex + snapshot
// swap is caught by -race.
func TestSnapshotReadsDuringRefresh(t *testing.T) {
	now := time.Now().UTC()
	src := &fakeSource{
		name: "fake",
		edges: []graph.Edge{
			{From: "USD", To: "ZAR", Rate: 18.5, Source: "fake", Time: now},
			{From: "EUR", To: "USD", Rate: 1.08, Source: "fake", Time: now},
			{From: "GBP", To: "USD", Rate: 1.27, Source: "fake", Time: now},
		},
	}

	// Very short interval so multiple refreshes fire during the test.
	st := New(5*time.Millisecond, src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go st.Run(ctx)

	const readers = 50
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				snap := st.Snapshot()
				if snap == nil {
					t.Errorf("Snapshot() returned nil")
					return
				}
				// Exercise the snapshot to ensure it is usable.
				_ = snap.Rebase("USD")
				_, _ = snap.Lookup("EUR", "ZAR")
			}
		}()
	}
	wg.Wait()
}

// TestStatusReadsDuringRefresh verifies that Status() is safe to call
// concurrently with ongoing refreshes.
func TestStatusReadsDuringRefresh(t *testing.T) {
	now := time.Now().UTC()
	src := &fakeSource{
		name: "fake",
		edges: []graph.Edge{
			{From: "USD", To: "ZAR", Rate: 18.0, Source: "fake", Time: now},
		},
	}

	st := New(5*time.Millisecond, src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go st.Run(ctx)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				statuses := st.Status()
				if len(statuses) == 0 {
					t.Errorf("Status() returned empty slice")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestConcurrentSnapshotAndStatus exercises Snapshot() and Status() together
// against concurrent refreshes to catch any lock contention or race.
func TestConcurrentSnapshotAndStatus(t *testing.T) {
	now := time.Now().UTC()
	src := &fakeSource{
		name: "fx",
		edges: []graph.Edge{
			{From: "USD", To: "EUR", Rate: 0.92, Source: "fx", Time: now},
		},
	}

	st := New(5*time.Millisecond, src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go st.Run(ctx)

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = st.Snapshot()
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				_ = st.Status()
			}
		}()
	}
	wg.Wait()
}
