package graph

import (
	"math"
	"testing"
	"time"
)

var (
	tBase = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tOld  = tBase.Add(-2 * time.Hour) // older
	tNew  = tBase.Add(2 * time.Hour)  // newer
)

func e(from, to string, rate float64, src string, at time.Time) Edge {
	return Edge{From: from, To: to, Rate: rate, Source: src, Time: at}
}

// ─── Self-conversion ─────────────────────────────────────────────────────────

func TestSelfConversionKnownCurrency(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{e("USD", "ZAR", 18.5, "ecb", tBase)})
	snap := g.Materialize(tBase)

	p, ok := snap.Lookup("USD", "USD")
	if !ok {
		t.Fatal("self-lookup must return ok=true")
	}
	if p.Rate != 1 {
		t.Errorf("self-conversion rate = %v, want 1", p.Rate)
	}
	if p.Hops != 0 {
		t.Errorf("self-conversion hops = %d, want 0", p.Hops)
	}
	if len(p.Path) != 1 || p.Path[0] != "USD" {
		t.Errorf("self-conversion path = %v, want [USD]", p.Path)
	}
}

// Snapshot.Lookup returns ok=true for any from==to regardless of whether the
// currency is in the graph — it's the mathematical identity.
func TestSelfConversionUnknownCurrency(t *testing.T) {
	g := New()
	snap := g.Materialize(tBase)
	p, ok := snap.Lookup("XYZ", "XYZ")
	if !ok {
		t.Fatal("self-lookup for any currency must return ok=true")
	}
	if p.Rate != 1 {
		t.Errorf("self-conversion rate = %v, want 1", p.Rate)
	}
}

// ─── Direct rates ────────────────────────────────────────────────────────────

func TestDirectRate(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{e("USD", "ZAR", 18.5, "ecb", tBase)})
	snap := g.Materialize(tBase)

	p, ok := snap.Lookup("USD", "ZAR")
	if !ok {
		t.Fatal("USD->ZAR must be reachable")
	}
	if p.Rate != 18.5 {
		t.Errorf("rate = %v, want 18.5", p.Rate)
	}
	if p.Hops != 1 {
		t.Errorf("hops = %d, want 1", p.Hops)
	}
}

func TestInverseEdgeAutoPopulated(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{e("USD", "ZAR", 18.5, "ecb", tBase)})
	snap := g.Materialize(tBase)

	p, ok := snap.Lookup("ZAR", "USD")
	if !ok {
		t.Fatal("ZAR->USD inverse must be reachable")
	}
	want := 1.0 / 18.5
	if diff := math.Abs(p.Rate - want); diff > 1e-12 {
		t.Errorf("ZAR->USD rate = %v, want %v (1/18.5)", p.Rate, want)
	}
	if p.Hops != 1 {
		t.Errorf("inverse hops = %d, want 1", p.Hops)
	}
}

// ─── Triangulated (multi-hop) paths ─────────────────────────────────────────

func TestTriangulatedTwoHops(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{
		e("USD", "EUR", 0.92, "ecb", tBase),
		e("EUR", "ZAR", 20.0, "ecb", tBase),
	})
	snap := g.Materialize(tBase)

	p, ok := snap.Lookup("USD", "ZAR")
	if !ok {
		t.Fatal("USD->ZAR via EUR must be reachable")
	}
	want := 0.92 * 20.0
	if diff := math.Abs(p.Rate - want); diff > 1e-10 {
		t.Errorf("triangulated rate = %v, want %v", p.Rate, want)
	}
	if p.Hops != 2 {
		t.Errorf("hops = %d, want 2", p.Hops)
	}
	if len(p.Path) != 3 {
		t.Errorf("path = %v, want [USD EUR ZAR]", p.Path)
	}
	if p.Path[0] != "USD" || p.Path[1] != "EUR" || p.Path[2] != "ZAR" {
		t.Errorf("path = %v, want [USD EUR ZAR]", p.Path)
	}
}

// Direct path must beat a shorter-arithmetic triangulated path.
func TestDirectBeatsTriangulated(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{
		e("USD", "ZAR", 18.5, "ecb", tBase), // 1-hop direct
		e("USD", "EUR", 0.92, "ecb", tBase), // these two produce 23.0
		e("EUR", "ZAR", 25.0, "ecb", tBase), // via EUR (2-hop)
	})
	snap := g.Materialize(tBase)

	p, ok := snap.Lookup("USD", "ZAR")
	if !ok {
		t.Fatal("USD->ZAR must be reachable")
	}
	if p.Hops != 1 {
		t.Errorf("direct edge must win: hops = %d, want 1", p.Hops)
	}
	if p.Rate != 18.5 {
		t.Errorf("direct rate = %v, want 18.5", p.Rate)
	}
}

// ─── Invalid-rate filtering ──────────────────────────────────────────────────

func TestZeroRateDropped(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{
		{From: "USD", To: "ZAR", Rate: 0, Source: "ecb", Time: tBase},
		e("USD", "EUR", 0.92, "ecb", tBase),
	})
	snap := g.Materialize(tBase)

	if _, ok := snap.Lookup("USD", "ZAR"); ok {
		t.Error("zero-rate edge must be dropped")
	}
	if _, ok := snap.Lookup("USD", "EUR"); !ok {
		t.Error("valid edge USD->EUR must still be reachable")
	}
}

func TestNegativeRateDropped(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{{From: "USD", To: "ZAR", Rate: -5, Source: "ecb", Time: tBase}})
	snap := g.Materialize(tBase)

	if _, ok := snap.Lookup("USD", "ZAR"); ok {
		t.Error("negative-rate edge must be dropped")
	}
}

// ─── Freshness tie-breaking ──────────────────────────────────────────────────

// When two sources publish the same pair, the fresher (later timestamp) source
// wins because BFS visits adjacency-list entries sorted newest-first.
func TestFreshestSourceWinsAmongSameHopCount(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{e("USD", "ZAR", 18.0, "ecb", tOld)})   // older
	g.Replace("sarb", []Edge{e("USD", "ZAR", 18.9, "sarb", tNew)}) // newer
	snap := g.Materialize(tBase)

	p, ok := snap.Lookup("USD", "ZAR")
	if !ok {
		t.Fatal("USD->ZAR must be reachable")
	}
	if p.Rate != 18.9 {
		t.Errorf("fresher source must win: rate = %v, want 18.9 (sarb, newer)", p.Rate)
	}
}

// ─── AsOf provenance ─────────────────────────────────────────────────────────

func TestAsOfIsOldestEdgeOnPath(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{
		e("USD", "EUR", 0.92, "ecb", tOld), // older
		e("EUR", "ZAR", 20.0, "ecb", tNew), // newer
	})
	snap := g.Materialize(tBase)

	p, ok := snap.Lookup("USD", "ZAR")
	if !ok {
		t.Fatal("USD->ZAR must be reachable")
	}
	if !p.AsOf.Equal(tOld) {
		t.Errorf("AsOf = %v, want %v (oldest edge on path)", p.AsOf, tOld)
	}
}

// ─── Unreachability ──────────────────────────────────────────────────────────

func TestUnreachablePair(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{e("USD", "EUR", 0.92, "ecb", tBase)})
	snap := g.Materialize(tBase)

	if _, ok := snap.Lookup("USD", "ZAR"); ok {
		t.Error("USD->ZAR is disconnected and must not be reachable")
	}
}

func TestEmptyGraph(t *testing.T) {
	g := New()
	snap := g.Materialize(tBase)

	if len(snap.Currencies) != 0 {
		t.Errorf("empty graph currencies = %v, want []", snap.Currencies)
	}
}

// ─── Rebase ──────────────────────────────────────────────────────────────────

func TestRebaseContainsAllOtherCurrencies(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{
		e("USD", "EUR", 0.92, "ecb", tBase),
		e("USD", "ZAR", 18.5, "ecb", tBase),
	})
	snap := g.Materialize(tBase)

	based := snap.Rebase("USD")
	if _, ok := based["USD"]; ok {
		t.Error("Rebase result must not include the base currency itself")
	}
	for _, want := range []string{"EUR", "ZAR"} {
		if _, ok := based[want]; !ok {
			t.Errorf("%s must appear in Rebase(USD)", want)
		}
	}
}

// ─── DirectQuotes ────────────────────────────────────────────────────────────

func TestDirectQuotesBothDirections(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{e("USD", "ZAR", 18.5, "ecb", tBase)})
	g.Replace("sarb", []Edge{e("USD", "ZAR", 18.6, "sarb", tOld)})
	snap := g.Materialize(tBase)

	if fwd := snap.DirectQuotes("USD", "ZAR"); len(fwd) != 2 {
		t.Errorf("DirectQuotes(USD,ZAR) = %d, want 2", len(fwd))
	}
	// Inverse direction auto-populated.
	if inv := snap.DirectQuotes("ZAR", "USD"); len(inv) != 2 {
		t.Errorf("DirectQuotes(ZAR,USD) = %d, want 2 (inverse auto-added)", len(inv))
	}
}

func TestDirectQuotesUnknownPairIsNil(t *testing.T) {
	g := New()
	snap := g.Materialize(tBase)
	if q := snap.DirectQuotes("USD", "ZAR"); q != nil {
		t.Errorf("unknown pair DirectQuotes = %v, want nil", q)
	}
}

// ─── Source deduplication ────────────────────────────────────────────────────

func TestSourcesDeduplicatedOnPath(t *testing.T) {
	// Both edges from the same source — Sources list must have exactly one entry.
	g := New()
	g.Replace("ecb", []Edge{
		e("USD", "EUR", 0.92, "ecb", tBase),
		e("EUR", "ZAR", 20.0, "ecb", tBase),
	})
	snap := g.Materialize(tBase)

	p, _ := snap.Lookup("USD", "ZAR")
	if len(p.Sources) != 1 || p.Sources[0] != "ecb" {
		t.Errorf("Sources = %v, want [ecb] (deduplicated)", p.Sources)
	}
}

func TestSourcesMultipleOnPath(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{e("USD", "EUR", 0.92, "ecb", tBase)})
	g.Replace("sarb", []Edge{e("EUR", "ZAR", 20.0, "sarb", tBase)})
	snap := g.Materialize(tBase)

	p, ok := snap.Lookup("USD", "ZAR")
	if !ok {
		t.Fatal("USD->ZAR via EUR must be reachable")
	}
	if len(p.Sources) != 2 {
		t.Errorf("Sources = %v, want 2 distinct sources", p.Sources)
	}
}

// ─── Replace / clear ─────────────────────────────────────────────────────────

func TestReplaceSourceClearsEdges(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{e("USD", "ZAR", 18.5, "ecb", tBase)})
	g.Replace("ecb", nil) // clear ecb's contribution
	snap := g.Materialize(tBase)

	if _, ok := snap.Lookup("USD", "ZAR"); ok {
		t.Error("cleared source must not leave stale edges behind")
	}
}

// ─── Currencies list ─────────────────────────────────────────────────────────

func TestCurrenciesAreSorted(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{
		e("ZAR", "USD", 0.054, "ecb", tBase),
		e("EUR", "USD", 1.08, "ecb", tBase),
	})
	snap := g.Materialize(tBase)

	prev := ""
	for _, c := range snap.Currencies {
		if c < prev {
			t.Errorf("currencies not sorted: %q comes after %q", c, prev)
		}
		prev = c
	}
}

// ─── Legs provenance ─────────────────────────────────────────────────────────

func TestLegsCarryProvenance(t *testing.T) {
	g := New()
	g.Replace("ecb", []Edge{
		e("USD", "EUR", 0.92, "ecb", tBase),
		e("EUR", "ZAR", 20.0, "ecb", tBase),
	})
	snap := g.Materialize(tBase)

	p, _ := snap.Lookup("USD", "ZAR")
	if len(p.Legs) != 2 {
		t.Fatalf("legs = %d, want 2 for a 2-hop path", len(p.Legs))
	}
	if p.Legs[0].From != "USD" || p.Legs[0].To != "EUR" {
		t.Errorf("leg[0] = %v->%v, want USD->EUR", p.Legs[0].From, p.Legs[0].To)
	}
	if p.Legs[1].From != "EUR" || p.Legs[1].To != "ZAR" {
		t.Errorf("leg[1] = %v->%v, want EUR->ZAR", p.Legs[1].From, p.Legs[1].To)
	}
}
