package embedtest

import (
	"context"
	"net/http"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fxsource"
)

// allSourceNames is every adapter fxsource ships. It is written out rather than
// derived so the guard has a coverage floor: Build silently skips names it does
// not know, so a renamed or deleted adapter shows up as a short list and fails
// the count assertion below instead of quietly shrinking what gets instrumented.
var allSourceNames = []string{
	"ecb", "coinbase", "luno", "sarb", "frankfurter", "yahoo", "erapi",
	"fawazahmed0", "boc", "oxr", "twelvedata", "polygon", "tradermade",
}

// paidKeyEnv is the set of variables that used to auto-enable a paid provider
// wherever sources were built — the concrete regression G2 exists for. A host
// that happens to carry an OXR key in its environment must not find itself
// paying for calls it never configured.
var paidKeyEnv = []string{
	"OPENRATE_OXR_APP_ID",
	"OPENRATE_TWELVEDATA_KEY",
	"OPENRATE_POLYGON_KEY",
	"OPENRATE_TRADERMADE_KEY",
}

// recordingTransport counts outbound round trips and performs none of them. Any
// request that reaches it is a bug being caught, so it never returns a response.
type recordingTransport struct {
	n    atomic.Int64
	last atomic.Value // string: the URL of the most recent attempt
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.n.Add(1)
	rt.last.Store(req.URL.String())
	return nil, errBlocked
}

func (rt *recordingTransport) count() int64 { return rt.n.Load() }

func (rt *recordingTransport) lastURL() string {
	if s, ok := rt.last.Load().(string); ok {
		return s
	}
	return "(none)"
}

var errBlocked = &blockedError{}

type blockedError struct{}

func (*blockedError) Error() string {
	return "embedtest: outbound request blocked — this transport exists to count, not to fetch"
}

// instrument routes every source's HTTP traffic through rt, and returns how
// many sources it managed to instrument.
//
// It fails the test for any source it cannot see inside. That matters: the
// zero-round-trip assertion is only as wide as the traffic this function can
// observe, so a new adapter that holds its *http.Client somewhere else must
// break the guard loudly rather than travel outside it.
func instrument(t *testing.T, srcs []fxsource.Source, rt http.RoundTripper) int {
	t.Helper()
	clientType := reflect.TypeOf((*http.Client)(nil))
	n := 0
	for _, s := range srcs {
		v := reflect.ValueOf(s)
		if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
			t.Fatalf("source %q is not a pointer to a struct; this guard cannot reach its HTTP client", s.Name())
		}
		f := v.Elem().FieldByName("Client")
		if !f.IsValid() || !f.CanSet() || f.Type() != clientType {
			t.Fatalf("source %q has no settable `Client *http.Client` field, so its outbound "+
				"traffic is invisible to the zero-round-trip guard. Give it one, or teach "+
				"instrument() how to see it — do not leave it uncounted.", s.Name())
		}
		f.Set(reflect.ValueOf(&http.Client{Transport: rt}))
		n++
	}
	return n
}

// TestG2ColdConstructionMakesZeroRoundTrips counts packets. It does not infer
// them from the absence of an error.
//
// Every adapter openrate ships is built and rewired through a counting
// transport, and http.DefaultTransport and http.DefaultClient are swapped for
// the same counter so a stray http.Get on some other path is caught too. Then
// an Engine and a Refresher over all thirteen sources are constructed, and the
// count must be exactly zero.
//
// The control at the end is not decoration. A counter that cannot count would
// report zero forever, which is indistinguishable from a pass, so the test
// finishes by deliberately fetching once and requiring the number to move.
func TestG2ColdConstructionMakesZeroRoundTrips(t *testing.T) {
	rt := &recordingTransport{}

	oldDefault := http.DefaultTransport
	oldClient := http.DefaultClient
	http.DefaultTransport = rt
	http.DefaultClient = &http.Client{Transport: rt}
	t.Cleanup(func() {
		http.DefaultTransport = oldDefault
		http.DefaultClient = oldClient
	})

	srcs := fxsource.Build(joinNames(allSourceNames))
	if len(srcs) != len(allSourceNames) {
		t.Fatalf("Build resolved %d of %d named sources — the ones it dropped would be "+
			"constructed by nobody and counted by nobody", len(srcs), len(allSourceNames))
	}
	if n := instrument(t, srcs, rt); n != len(allSourceNames) {
		t.Fatalf("instrumented %d of %d sources", n, len(allSourceNames))
	}

	// The construction under test. Nothing else happens.
	e := openrate.NewEngine(openrate.EngineOptions{Base: "ZAR", Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{
		Sources:  srcs,
		Interval: time.Hour,
		Logger:   quiet(),
	})

	// Give anything that intended to fire asynchronously the chance to do so.
	time.Sleep(100 * time.Millisecond)

	if got := rt.count(); got != 0 {
		t.Fatalf("constructing an Engine and a Refresher made %d HTTP round trip(s); last was %s.\n"+
			"A library that reaches the network before it is asked to cannot be embedded behind "+
			"a feature flag that is off.", got, rt.lastURL())
	}
	if len(e.Snapshot().Currencies) != 0 {
		t.Errorf("the engine holds %d currencies without anyone having fetched",
			len(e.Snapshot().Currencies))
	}
	if st := r.Status(); len(st) != len(allSourceNames) {
		t.Errorf("Status reports %d sources, want %d", len(st), len(allSourceNames))
	} else {
		for _, s := range st {
			if !s.LastOK.IsZero() || s.Edges != 0 {
				t.Errorf("source %q reports a completed fetch (%d edges at %s) before anything ran",
					s.Name, s.Edges, s.LastOK)
			}
		}
	}

	// CONTROL. Fetch once, on purpose, and require the counter to move. Without
	// this the assertion above passes just as happily against a transport that
	// was never wired to anything.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = srcs[0].Fetch(ctx)
	if got := rt.count(); got < 1 {
		t.Fatalf("a deliberate %s fetch produced %d round trips — the counter is not wired to "+
			"anything, so the zero above measured nothing", srcs[0].Name(), got)
	}
}

// TestG2ColdConstructionStartsNoGoroutines asserts a constructor leaves the
// process's goroutine count where it found it.
//
// It starts with a sensitivity control for the same reason as above: a
// measurement that cannot see a goroutine appear reports a clean bill of health
// no matter what a constructor does.
func TestG2ColdConstructionStartsNoGoroutines(t *testing.T) {
	base := stableGoroutines(t)
	release := make(chan struct{})
	go func() { <-release }()
	if !waitUntil(2*time.Second, func() bool { return runtime.NumGoroutine() > base }) {
		close(release)
		t.Fatal("a goroutine that certainly started was not visible to runtime.NumGoroutine; " +
			"this measurement would pass whatever a constructor did")
	}
	close(release)

	before := stableGoroutines(t)

	srcs := fxsource.Build(joinNames(allSourceNames))
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	r := openrate.NewRefresher(e, openrate.RefreshOptions{Sources: srcs, Logger: quiet()})

	// Settle again rather than sleeping a fixed amount: a goroutine a
	// constructor started is still running, so the settled count stays high,
	// while unrelated churn from an earlier test gets a chance to drain.
	after := stableGoroutines(t)
	if after > before {
		t.Fatalf("constructing an Engine and a Refresher left %d extra goroutine(s) running "+
			"(%d -> %d). Constructing the library must cost a host nothing until it asks "+
			"for something.", after-before, before, after)
	}
	runtime.KeepAlive(r)
}

// TestG2ColdConstructionAdoptsNoEnvironmentKey is the openrate-specific half of
// rule 2, and it is the exact bug that motivated the split: sources.Build used
// to consult the environment, so any process carrying an OXR or Polygon key
// silently acquired a paid provider it never configured.
//
// Every paid key is set to a sentinel here, plus OPENRATE_SOURCES and
// OPENRATE_BASE, and the library must ignore all of them. fxsource.FromEnv is
// then called as a control, because it is the one function that is SUPPOSED to
// read them — if it does not adopt the sentinels either, the variables were
// never visible and the assertions above were vacuous.
func TestG2ColdConstructionAdoptsNoEnvironmentKey(t *testing.T) {
	for _, k := range paidKeyEnv {
		t.Setenv(k, "sentinel-key-that-must-not-enable-anything")
	}
	t.Setenv("OPENRATE_SOURCES", "polygon,twelvedata")
	t.Setenv("OPENRATE_BASE", "JPY")
	t.Setenv("OPENRATE_REFRESH", "1s")

	// Build is the library path: pure, spec in, sources out.
	got := names(fxsource.Build("ecb"))
	if len(got) != 1 || got[0] != "ecb" {
		t.Errorf("Build(\"ecb\") returned %v with paid keys in the environment; want exactly [ecb]", got)
	}
	if def := names(fxsource.Build("")); !sameSet(def, fxsource.DefaultSources) {
		t.Errorf("Build(\"\") returned %v; want the compiled-in defaults %v, not whatever "+
			"OPENRATE_SOURCES happens to say", def, fxsource.DefaultSources)
	}

	// The constructors themselves.
	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	if got := e.DefaultBase(); got != "ZAR" {
		t.Errorf("NewEngine picked up base %q; OPENRATE_BASE is the binary's business, not the library's", got)
	}
	r := openrate.NewRefresher(e, openrate.RefreshOptions{Sources: fxsource.Build("ecb"), Logger: quiet()})
	st := r.Status()
	if len(st) != 1 || st[0].Name != "ecb" {
		t.Errorf("a Refresher built with one source reports %d source(s) %v; the environment widened its set",
			len(st), statusNames(st))
	}

	// CONTROL. FromEnv is the documented opt-in, and it must adopt every
	// sentinel: four paid keys on top of the defaults, because the sentinel
	// OPENRATE_SOURCES names two paid providers whose keys are also set.
	env := names(fxsource.FromEnv())
	for _, want := range []string{"oxr", "twelvedata", "polygon", "tradermade"} {
		if !contains(env, want) {
			t.Fatalf("fxsource.FromEnv() returned %v and did not adopt %q — the sentinel "+
				"variables are not visible to this process, so nothing above was actually tested", env, want)
		}
	}
}

// stableGoroutines waits until the process's goroutine count stops moving and
// returns it. A bare sample is a flaky baseline: an earlier test's httptest
// server or fetch can still be winding down, and its exit would be misread as
// this constructor's doing.
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

func waitUntil(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

func joinNames(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ","
		}
		out += x
	}
	return out
}

func names(srcs []fxsource.Source) []string {
	out := make([]string, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, s.Name())
	}
	return out
}

func statusNames(st []openrate.SourceStatus) []string {
	out := make([]string, 0, len(st))
	for _, s := range st {
		out = append(out, s.Name)
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range b {
		if !contains(a, x) {
			return false
		}
	}
	return true
}
