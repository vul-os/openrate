package openrate_test

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/fxsource"
)

// sources adapts test doubles to the explicit []fxsource.Source a Refresher
// takes. There is no registry lookup and no environment in this file: every
// test says exactly what it fetches from.
func sources(ss ...*fakeSource) []fxsource.Source {
	out := make([]fxsource.Source, 0, len(ss))
	for _, s := range ss {
		out = append(out, s)
	}
	return out
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// fakeSource is an fxsource.Source (structurally — no import needed) that
// returns a fixed set of edges instantly, so tests drive a Refresher with no
// network I/O at all.
type fakeSource struct {
	name    string
	edges   []fx.Edge
	err     error
	delay   time.Duration
	fetches atomic.Int64
}

func (f *fakeSource) Name() string { return f.name }
func (f *fakeSource) Fetch(ctx context.Context) ([]fx.Edge, error) {
	f.fetches.Add(1)
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.edges, f.err
}

func edgesAt(t time.Time) []fx.Edge {
	return []fx.Edge{
		{From: "USD", To: "ZAR", Rate: 18.5, Source: "fake", Time: t},
		{From: "EUR", To: "USD", Rate: 1.08, Source: "fake", Time: t},
		{From: "GBP", To: "USD", Rate: 1.27, Source: "fake", Time: t},
	}
}

// TestConstructorsSendNothing is the structural guarantee the split exists for:
// building an Engine and even building a Refresher over it must not start a
// goroutine and must not call Fetch. Only Refresh/Run may.
//
// beepbite refused to embed openrate because Start "boots the full engine
// in-process, which would put a refresh loop and outbound source fetches inside
// the POS server whether or not the feature is on". This test is that refusal,
// inverted into an assertion.
func TestConstructorsSendNothing(t *testing.T) {
	src := &fakeSource{name: "fake", edges: edgesAt(fixtureTime)}

	before := stableGoroutines(t)

	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{Sources: sources(src), Logger: quiet()})
	_ = r

	// Give anything that was going to start a chance to start.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("constructing an Engine and a Refresher started %d goroutine(s)", after-before)
	}
	if n := src.fetches.Load(); n != 0 {
		t.Errorf("constructors called Fetch %d time(s); they must never touch the network", n)
	}
	if len(e.Snapshot().Currencies) != 0 {
		t.Error("the engine acquired rates without anyone asking it to fetch")
	}
}

func TestRefreshLoadsTheEngine(t *testing.T) {
	src := &fakeSource{name: "fake", edges: edgesAt(fixtureTime)}
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{Sources: sources(src), Logger: quiet()})

	if err := r.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if n := src.fetches.Load(); n != 1 {
		t.Errorf("Fetch called %d times, want exactly 1", n)
	}
	c, err := e.Convert("USD", "ZAR", 2)
	if err != nil {
		t.Fatalf("Convert after Refresh: %v", err)
	}
	if c.Result != 37 {
		t.Errorf("2 USD→ZAR = %v, want 37", c.Result)
	}

	st := r.Status()
	if len(st) != 1 || st[0].Name != "fake" || st[0].Edges != 3 || st[0].LastError != "" {
		t.Fatalf("Status() = %+v, want one healthy 'fake' with 3 edges", st)
	}
	if st[0].LastOK.IsZero() {
		t.Error("a successful fetch left LastOK zero")
	}
}

// A failing source must not take the snapshot down with it, and its error must
// be visible in Status rather than only in a log line.
func TestRefreshPartialFailure(t *testing.T) {
	good := &fakeSource{name: "good", edges: edgesAt(fixtureTime)}
	bad := &fakeSource{name: "bad", err: errors.New("boom")}
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{Sources: sources(good, bad), Logger: quiet()})

	if err := r.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh with one healthy source must succeed, got %v", err)
	}
	if _, err := e.Convert("USD", "ZAR", 1); err != nil {
		t.Errorf("the healthy source's rates are missing: %v", err)
	}
	byName := map[string]openrate.SourceStatus{}
	for _, s := range r.Status() {
		byName[s.Name] = s
	}
	if byName["bad"].LastError == "" {
		t.Error("the failed source recorded no error in Status")
	}
	if byName["good"].LastError != "" {
		t.Errorf("the healthy source recorded an error: %q", byName["good"].LastError)
	}
}

func TestRefreshAllSourcesFailing(t *testing.T) {
	bad := &fakeSource{name: "bad", err: errors.New("boom")}
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{Sources: sources(bad), Logger: quiet()})

	err := r.Refresh(context.Background())
	if err == nil {
		t.Fatal("a refresh in which every source failed reported success")
	}
	if !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("the error names neither the source nor the cause: %v", err)
	}
}

func TestRefreshWithNoSources(t *testing.T) {
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{Logger: quiet()})
	if err := r.Refresh(context.Background()); !errors.Is(err, openrate.ErrNoSources) {
		t.Errorf("Refresh with no sources: err = %v, want ErrNoSources", err)
	}
	if err := r.Run(context.Background()); !errors.Is(err, openrate.ErrNoSources) {
		t.Errorf("Run with no sources: err = %v, want ErrNoSources", err)
	}
	if st := r.Status(); len(st) != 0 {
		t.Errorf("Status() = %+v, want empty", st)
	}
}

// FetchTimeout must be honoured, and must be the caller's number: the 50s
// default used to be hard-coded inside the store, which made a host wait almost
// a minute for a hung source it would rather have given 200ms.
func TestRefreshFetchTimeoutIsHonoured(t *testing.T) {
	slow := &fakeSource{name: "slow", edges: edgesAt(fixtureTime), delay: 30 * time.Second}
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{
		Sources: sources(slow), FetchTimeout: 50 * time.Millisecond, Logger: quiet(),
	})

	start := time.Now()
	err := r.Refresh(context.Background())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a source that never answers within the timeout reported success")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Refresh took %s — the configured 50ms timeout was ignored", elapsed)
	}
}

// Ready is the signal the health endpoint never gave.
func TestReadyBlocksUntilTheFirstRates(t *testing.T) {
	src := &fakeSource{name: "fake", edges: edgesAt(fixtureTime), delay: 150 * time.Millisecond}
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{Sources: sources(src), Logger: quiet()})

	// Before anything fetches, Ready must NOT report ready.
	early, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := r.Ready(early); err == nil {
		t.Fatal("Ready returned nil before a single rate existed — that is the bug it exists to fix")
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = r.Run(ctx) }()

	wait, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := r.Ready(wait); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if len(e.Snapshot().Currencies) == 0 {
		t.Fatal("Ready returned but the engine still holds an empty snapshot")
	}
}

// Ready must stay satisfied: it is a latch, not a level.
func TestReadyIsALatch(t *testing.T) {
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{
		Sources: sources(&fakeSource{name: "fake", edges: edgesAt(fixtureTime)}), Logger: quiet(),
	})
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := r.Ready(ctx); err != nil {
			t.Fatalf("Ready call %d: %v", i, err)
		}
		cancel()
	}
}

// A snapshot loaded by hand makes the engine ready too — Ready is about rates
// existing, not about the network having been used.
func TestReadyAfterManualLoad(t *testing.T) {
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{Logger: quiet()})
	e.Load(fixtureSnapshot(t))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Ready(ctx); err != nil {
		t.Fatalf("Ready after Load: %v", err)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	src := &fakeSource{name: "fake", edges: edgesAt(fixtureTime)}
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{
		Sources: sources(src), Interval: 5 * time.Millisecond, Logger: quiet(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of cancellation")
	}
	if src.fetches.Load() < 2 {
		t.Errorf("the ticker fired %d times in 50ms at a 5ms interval — the loop is not looping", src.fetches.Load())
	}
}

// ─── Concurrency (carried from internal/store; only meaningful under -race) ───

// TestSnapshotReadsDuringRefresh fires many concurrent Snapshot() calls while
// the refresher is continuously refreshing. Any data race on the snapshot swap
// is caught by -race.
func TestSnapshotReadsDuringRefresh(t *testing.T) {
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{
		Sources:  sources(&fakeSource{name: "fake", edges: edgesAt(time.Now().UTC())}),
		Interval: 5 * time.Millisecond, Logger: quiet(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	const readers = 50
	var wg sync.WaitGroup
	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				snap := e.Snapshot()
				if snap == nil {
					t.Error("Snapshot() returned nil")
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
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{
		Sources:  sources(&fakeSource{name: "fake", edges: edgesAt(time.Now().UTC())}),
		Interval: 5 * time.Millisecond, Logger: quiet(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if len(r.Status()) == 0 {
					t.Error("Status() returned empty slice")
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
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{
		Sources:  sources(&fakeSource{name: "fx", edges: edgesAt(time.Now().UTC())}),
		Interval: 5 * time.Millisecond, Logger: quiet(),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 100 {
				_ = e.Snapshot()
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				_ = r.Status()
			}
		}()
	}
	wg.Wait()
}

// stableGoroutines waits until the process's goroutine count stops moving and
// returns it. Without this the count is a flaky baseline: an earlier test in
// the same binary (openrate.Start's refresh loop, an httptest server) can still
// be winding down, and its exit would be misread as our constructor's doing.
func stableGoroutines(t *testing.T) int {
	t.Helper()
	prev := -1
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(25 * time.Millisecond)
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}
		prev = n
	}
	t.Fatalf("goroutine count never settled (last %d); cannot measure what a constructor started", prev)
	return 0
}
