package ratestore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/vul-os/openrate/internal/rates"
)

// fakeSource is a ratesources.Source that returns a fixed set of observations
// instantly, allowing tests to drive the store without real network I/O.
type fakeSource struct {
	name string
	obs  []rates.Observation
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Fetch(_ context.Context) ([]rates.Observation, error) {
	return f.obs, nil
}

// TestSnapshotReadsDuringRefresh fires many concurrent Snapshot() calls while
// the store is continuously refreshing. Any data race on the RWMutex + snapshot
// swap is caught by -race.
func TestSnapshotReadsDuringRefresh(t *testing.T) {
	now := time.Now().UTC()
	src := &fakeSource{
		name: "fake",
		obs: []rates.Observation{
			{Series: "us.policy", Area: "US", Type: rates.TypePolicy, Name: "US Fed Funds Rate", Value: 5.25, Date: now, Source: "fake"},
			{Series: "za.ref.zaronia.on", Area: "ZA", Type: rates.TypeReference, Name: "ZARONIA ON", Value: 8.4, Date: now, Source: "fake"},
			{Series: "xm.ref.estr", Area: "XM", Type: rates.TypeReference, Name: "€STR", Value: 3.9, Date: now, Source: "fake"},
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
				_ = snap.IDs()
				_, _ = snap.Lookup("us.policy")
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
		obs: []rates.Observation{
			{Series: "us.policy", Area: "US", Type: rates.TypePolicy, Value: 5.25, Date: now, Source: "fake"},
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
		name: "fake",
		obs: []rates.Observation{
			{Series: "za.lending", Area: "ZA", Type: rates.TypeLending, Value: 11.75, Date: now, Source: "fake"},
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
