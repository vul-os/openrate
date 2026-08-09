package abi

import (
	"net/http"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// openrate's central claim is that computing and fetching are separate
// constructions, so a host that only computes sends no packets. embedtest's G2
// counts that for the Go API. This is the same count for the C ABI, because a
// property that holds in the library and not in the binding other languages
// actually load is a property those languages do not have.
//
// The sequence measured here is exactly the one a host in another language
// performs: openrate_new, openrate_call("load"), openrate_call("convert"), and
// the read methods around them. The cgo layer above this package converts
// strings and nothing else — it has no network code to add — so counting here
// counts the whole path. The C smoke test then makes the same assertion at the
// operating-system level, through a real dlopen'd library, by censusing socket
// file descriptors: see ffi/test/smoke.c.

type countingTransport struct {
	n    atomic.Int64
	last atomic.Value // string
}

func (rt *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.n.Add(1)
	rt.last.Store(req.URL.String())
	return nil, errCounted
}

func (rt *countingTransport) count() int64 { return rt.n.Load() }

func (rt *countingTransport) lastURL() string {
	if s, ok := rt.last.Load().(string); ok {
		return s
	}
	return "(none)"
}

type countedError struct{}

func (*countedError) Error() string {
	return "ffi test: outbound request blocked — this transport exists to count, not to fetch"
}

var errCounted = &countedError{}

func TestZeroPacketsThroughTheABI(t *testing.T) {
	rt := &countingTransport{}
	oldTransport, oldClient := http.DefaultTransport, http.DefaultClient
	http.DefaultTransport = rt
	http.DefaultClient = &http.Client{Transport: rt}
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
		http.DefaultClient = oldClient
	})

	// --- the sequence under test -------------------------------------------
	h, err := New(`{"base":"ZAR","quiet":true}`)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer Close(h)

	if _, err := Call(h, "load", testEdges); err != nil {
		t.Fatalf("load: %v", err)
	}
	out, err := Call(h, "convert", `{"from":"USD","to":"ZAR","amount":100}`)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	// The conversion must have actually happened. A path that errored out early
	// would also make zero round trips, and would prove nothing.
	if out == "" || !containsAll(out, `"result":1852`, `"rate":18.52`) {
		t.Fatalf("convert returned %s; the assertion below is only meaningful if a real "+
			"conversion was computed", out)
	}
	if _, err := Call(h, "rates", `{"base":"USD"}`); err != nil {
		t.Fatalf("rates: %v", err)
	}
	if _, err := Call(h, "meta", `{}`); err != nil {
		t.Fatalf("meta: %v", err)
	}

	// Constructing a Refresher is still construction. It resolves thirteen
	// possible adapters' worth of configuration and builds four real HTTP
	// clients, and it must still not use them.
	rh, err := NewRefresher(h, `{"sources":"ecb,coinbase,luno,sarb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}
	defer Close(rh)
	if _, err := Call(rh, "status", `{}`); err != nil {
		t.Fatalf("status: %v", err)
	}

	// Give anything that meant to fire asynchronously its chance.
	time.Sleep(100 * time.Millisecond)

	if got := rt.count(); got != 0 {
		t.Fatalf("the C ABI's engine path made %d HTTP round trip(s); the last was %s.\n"+
			"A host in another language that loads this library and only converts numbers must "+
			"be able to prove its process sends nothing. It cannot, as of this failure.",
			got, rt.lastURL())
	}

	// --- CONTROL ------------------------------------------------------------
	// A counter wired to nothing reports zero forever, which reads exactly like
	// a pass. Fetch once, deliberately, through the same default client the
	// assertion above trusted, and require the number to move.
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/openrate-control", nil)
	if err != nil {
		t.Fatalf("build the control request: %v", err)
	}
	_, _ = http.DefaultClient.Do(req)
	if got := rt.count(); got < 1 {
		t.Fatal("a deliberate request through http.DefaultClient produced 0 round trips. The " +
			"counter is not wired to anything, so the zero above measured nothing.")
	}
}

// TestEngineConstructionThroughTheABIStartsNoGoroutine is the other half of
// "costs a host nothing": no packets, and no thread either. A host that builds
// an engine behind a feature flag that is off must be able to say its process
// is unchanged.
func TestEngineConstructionThroughTheABIStartsNoGoroutine(t *testing.T) {
	// The refresher's loop is exercised at the end, so the "network" is the
	// stub from refresher_test.go rather than the real one.
	stubNetwork(t)

	// Sensitivity control first: a measurement that cannot see a goroutine
	// appear would report a clean bill of health whatever the ABI did.
	base := settled(t)
	release := make(chan struct{})
	go func() { <-release }()
	if !within(2*time.Second, func() bool { return runtime.NumGoroutine() > base }) {
		close(release)
		t.Fatal("a goroutine that certainly started was invisible to runtime.NumGoroutine; " +
			"this measurement would pass whatever the ABI started")
	}
	close(release)

	before := settled(t)

	h, err := New(`{"quiet":true}`)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := Call(h, "load", testEdges); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := Call(h, "convert", `{"from":"USD","to":"ZAR","amount":1}`); err != nil {
		t.Fatalf("convert: %v", err)
	}
	rh, err := NewRefresher(h, `{"sources":"ecb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}

	after := settled(t)
	if after > before {
		t.Errorf("the ABI left %d extra goroutine(s) running (%d -> %d) without anyone calling "+
			"\"start\". The background loop is supposed to be opt-in by name.", after-before, before, after)
	}

	// CONTROL. When a host DOES ask for the loop by name, a goroutine appears.
	// Without this the assertion above is equally satisfied by an ABI whose
	// "start" does nothing at all.
	if _, err := Call(rh, "start", `{}`); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !within(5*time.Second, func() bool { return runtime.NumGoroutine() > after }) {
		t.Error("\"start\" ran no goroutine, so the assertion above did not distinguish an " +
			"opt-in background loop from a broken one")
	}
	if _, err := Call(rh, "stop", `{}`); err != nil {
		t.Fatalf("stop: %v", err)
	}
	Close(h)
}

func settled(t *testing.T) int {
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
	t.Fatalf("goroutine count never settled (last %d)", prev)
	return 0
}

func within(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
