package sources

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/vul-os/openrate/fx"
)

// A feed can put the literal text "NaN" or "Inf" in a rate field, and
// strconv.ParseFloat accepts both with a nil error. BIS already publishes
// literal NaN for missing days, so this is a real wire shape, not a hypothetical.
//
// Every FX source guards its parse with `err != nil || rate <= 0`, which does NOT
// catch NaN or +Inf — every comparison with NaN is false. These tests pin where
// the value is actually stopped, so nobody deletes the guard in
// fx.adjacency believing the sources already handle it.

// TestParseFloatAcceptsNonFiniteText is the premise the guard exists for. If Go
// ever changed this, the graph guard would become dead code — and this test
// would say so.
func TestParseFloatAcceptsNonFiniteText(t *testing.T) {
	for _, s := range []string{"NaN", "nan", "Inf", "+Inf", "infinity"} {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("strconv.ParseFloat(%q) now errors (%v) — the `rate <= 0` guard in each source "+
				"would then be sufficient and fx's finite check could be revisited", s, err)
		}
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			t.Fatalf("strconv.ParseFloat(%q) = %v, expected a non-finite value", s, v)
		}
		// The exact hole: `rate <= 0` is false for NaN and +Inf, so the guard
		// every source uses lets them straight through.
		if !(v <= 0) && !math.IsNaN(v) && !math.IsInf(v, 1) {
			t.Fatalf("unexpected value for %q: %v", s, v)
		}
	}
	// Spelled out for the two that matter.
	nan, _ := strconv.ParseFloat("NaN", 64)
	if nan <= 0 {
		t.Error("NaN <= 0 should be false; if it is true, Go's comparison semantics changed")
	}
	inf, _ := strconv.ParseFloat("Inf", 64)
	if inf <= 0 {
		t.Error("+Inf <= 0 should be false")
	}
}

// TestECBNonFiniteRateNeverReachesASnapshot walks a literal "NaN" from the ECB
// wire format through to the materialized snapshot.
func TestECBNonFiniteRateNeverReachesASnapshot(t *testing.T) {
	const xml = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
 <Cube>
  <Cube time="2026-06-30">
   <Cube currency="USD" rate="1.0800"/>
   <Cube currency="ZAR" rate="NaN"/>
   <Cube currency="JPY" rate="Inf"/>
  </Cube>
 </Cube>
</gesmes:Envelope>`
	srv := serve(t, 200, xml)
	e := NewECB()
	e.URL = srv.URL
	edges, err := e.Fetch(ctx())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// Whatever the source chose to emit, the graph is the chokepoint that must
	// hold: no non-finite rate may appear in a materialized snapshot.
	g := fx.NewGraph()
	g.Replace("ecb", edges)
	snap := g.Materialize(time.Now().UTC())

	if _, ok := snap.Lookup("EUR", "USD"); !ok {
		t.Fatal("the well-formed EUR->USD rate must survive")
	}
	for _, ccy := range []string{"ZAR", "JPY"} {
		if p, ok := snap.Lookup("EUR", ccy); ok {
			t.Errorf("EUR->%s reached the snapshot with rate %v; a non-finite wire value must be dropped", ccy, p.Rate)
		}
	}
	for _, from := range snap.Currencies {
		for _, to := range snap.Currencies {
			if p, ok := snap.Lookup(from, to); ok {
				if math.IsNaN(p.Rate) || math.IsInf(p.Rate, 0) || p.Rate <= 0 {
					t.Errorf("%s->%s has unusable rate %v", from, to, p.Rate)
				}
			}
		}
	}
	if n := len(snap.Currencies); n != 2 {
		t.Errorf("expected exactly EUR and USD in the snapshot, got %v", snap.Currencies)
	}
}
