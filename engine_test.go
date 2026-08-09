package openrate_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
)

var fixtureTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// fixtureSnapshot is a small hand-built book: USD→ZAR direct, USD→EUR direct,
// so EUR→ZAR must triangulate.
func fixtureSnapshot(t *testing.T) *fx.Snapshot {
	t.Helper()
	g := fx.NewGraph()
	g.Replace("sarb", []fx.Edge{{From: "USD", To: "ZAR", Rate: 18.5, Source: "sarb", Time: fixtureTime}})
	g.Replace("ecb", []fx.Edge{{From: "USD", To: "EUR", Rate: 0.92, Source: "ecb", Time: fixtureTime}})
	snap := g.Materialize(fixtureTime)
	if len(snap.Currencies) != 3 {
		t.Fatalf("fixture has %d currencies, want 3 — the fixture is broken and these tests check nothing", len(snap.Currencies))
	}
	return snap
}

func newFixtureEngine(t *testing.T) *openrate.Engine {
	t.Helper()
	e := openrate.NewEngine(openrate.EngineOptions{Now: func() time.Time { return fixtureTime }})
	e.Load(fixtureSnapshot(t))
	return e
}

// TestNewEngineIsInert is the acceptance criterion for the whole library: an
// engine constructed in a host process that has the FX feature switched off
// must do nothing at all. No rates, no error, no packets — and, crucially, an
// honest "I don't know" rather than a zero.
func TestNewEngineIsInert(t *testing.T) {
	e := openrate.NewEngine(openrate.EngineOptions{})

	if snap := e.Snapshot(); snap == nil {
		t.Fatal("Snapshot() on a fresh engine is nil; every read path would have to nil-check")
	} else if len(snap.Currencies) != 0 {
		t.Fatalf("a fresh engine already knows %d currencies", len(snap.Currencies))
	}
	if _, err := e.Convert("USD", "ZAR", 1); !errors.Is(err, openrate.ErrUnknownPair) {
		t.Fatalf("Convert on an unloaded engine: err = %v, want ErrUnknownPair (never a zero rate)", err)
	}
	rates, err := e.Rates("USD")
	if err != nil {
		t.Fatalf("Rates on an unloaded engine: %v — 'nothing yet' is not a bad request", err)
	}
	if len(rates) != 0 {
		t.Fatalf("Rates on an unloaded engine returned %d entries", len(rates))
	}
}

func TestEngineDefaultBase(t *testing.T) {
	if got := openrate.NewEngine(openrate.EngineOptions{}).DefaultBase(); got != "ZAR" {
		t.Errorf("default base = %q, want ZAR", got)
	}
	if got := openrate.NewEngine(openrate.EngineOptions{Base: "USD"}).DefaultBase(); got != "USD" {
		t.Errorf("base = %q, want USD", got)
	}
}

func TestEngineConvert(t *testing.T) {
	e := newFixtureEngine(t)

	c, err := e.Convert("USD", "ZAR", 100)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if c.Result != 1850 {
		t.Errorf("100 USD→ZAR = %v, want 1850", c.Result)
	}
	if c.Hops != 1 || len(c.Legs) != 1 {
		t.Errorf("USD→ZAR is direct: hops = %d, legs = %d", c.Hops, len(c.Legs))
	}
	if c.Quality.Grade == "" {
		t.Error("Convert returned no grade")
	}

	// Lower case and stray whitespace are the same currency.
	if lower, err := e.Convert(" usd ", "zar", 100); err != nil || lower.Result != c.Result {
		t.Errorf("lower-case codes: %v / %v, want the same answer as USD→ZAR", lower.Result, err)
	}

	// Triangulation still carries the exact product of its legs.
	cross, err := e.Convert("EUR", "ZAR", 1)
	if err != nil {
		t.Fatalf("Convert EUR→ZAR: %v", err)
	}
	if cross.Hops != 2 {
		t.Fatalf("EUR→ZAR hops = %d, want 2", cross.Hops)
	}
	product := 1.0
	for _, l := range cross.Legs {
		product *= l.Rate
	}
	if product != cross.Rate {
		t.Errorf("EUR→ZAR legs multiply to %v, rate is %v — not bit-for-bit", product, cross.Rate)
	}
}

func TestEngineConvertErrors(t *testing.T) {
	e := newFixtureEngine(t)
	if _, err := e.Convert("USD", "XXX", 1); !errors.Is(err, openrate.ErrUnknownPair) {
		t.Errorf("unknown pair: err = %v, want ErrUnknownPair", err)
	}
	if _, err := e.Convert("USD", "ZAR", math.NaN()); !errors.Is(err, openrate.ErrInvalidAmount) {
		t.Errorf("NaN amount: err = %v, want ErrInvalidAmount", err)
	}
	if _, err := e.Convert("USD", "ZAR", math.MaxFloat64); !errors.Is(err, openrate.ErrAmountOutOfRange) {
		t.Errorf("overflowing amount: err = %v, want ErrAmountOutOfRange", err)
	}
}

func TestEngineRates(t *testing.T) {
	e := newFixtureEngine(t)

	rates, err := e.Rates("USD")
	if err != nil {
		t.Fatalf("Rates: %v", err)
	}
	if len(rates) != 2 {
		t.Fatalf("Rates(USD) returned %d entries, want 2 (ZAR, EUR)", len(rates))
	}
	if rates["ZAR"].Rate != 18.5 {
		t.Errorf("1 USD = %v ZAR, want 18.5", rates["ZAR"].Rate)
	}

	// An empty base means the engine's configured default.
	if def, err := e.Rates(""); err != nil || len(def) != 2 {
		t.Errorf(`Rates("") = %d entries, %v — want the default base's book`, len(def), err)
	}
	if _, err := e.Rates("XXX"); !errors.Is(err, openrate.ErrUnknownBase) {
		t.Errorf("Rates(XXX): err = %v, want ErrUnknownBase", err)
	}
}

// TestEngineLoadReplaces: Load is the zero-network path, and a second Load must
// fully replace the book rather than merge into it.
func TestEngineLoadReplaces(t *testing.T) {
	e := newFixtureEngine(t)
	if _, err := e.Convert("USD", "ZAR", 1); err != nil {
		t.Fatalf("precondition: %v", err)
	}

	g := fx.NewGraph()
	g.Replace("other", []fx.Edge{{From: "GBP", To: "JPY", Rate: 190, Source: "other", Time: fixtureTime}})
	e.Load(g.Materialize(fixtureTime))

	if _, err := e.Convert("USD", "ZAR", 1); !errors.Is(err, openrate.ErrUnknownPair) {
		t.Error("the replaced snapshot still answers for a pair only the old one knew")
	}
	c, err := e.Convert("GBP", "JPY", 2)
	if err != nil {
		t.Fatalf("Convert on the new snapshot: %v", err)
	}
	if c.Result != 380 {
		t.Errorf("2 GBP→JPY = %v, want 380", c.Result)
	}
}

func TestEngineLoadNilIsIgnored(t *testing.T) {
	e := newFixtureEngine(t)
	e.Load(nil)
	if _, err := e.Convert("USD", "ZAR", 1); err != nil {
		t.Fatalf("Load(nil) discarded the engine's snapshot: %v", err)
	}
}

// The injected clock must actually be the clock: ages and grades are computed
// from it, so a pinned clock yields a reproducible answer.
func TestEngineUsesInjectedClock(t *testing.T) {
	snap := fixtureSnapshot(t)
	later := fixtureTime.Add(48 * time.Hour)

	fresh := openrate.NewEngine(openrate.EngineOptions{Now: func() time.Time { return fixtureTime }})
	fresh.Load(snap)
	stale := openrate.NewEngine(openrate.EngineOptions{Now: func() time.Time { return later }})
	stale.Load(snap)

	a, err := fresh.Convert("USD", "ZAR", 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := stale.Convert("USD", "ZAR", 1)
	if err != nil {
		t.Fatal(err)
	}
	if a.AgeSec != 0 {
		t.Errorf("age at the snapshot's own instant = %v, want 0", a.AgeSec)
	}
	if b.AgeSec != (48 * time.Hour).Seconds() {
		t.Errorf("age two days later = %v, want %v", b.AgeSec, (48 * time.Hour).Seconds())
	}
	if a.Quality.Freshness == b.Quality.Freshness {
		t.Errorf("both clocks graded freshness %q — the injected clock is not being used", a.Quality.Freshness)
	}
}
