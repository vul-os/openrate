package fx

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// openrate does its rate arithmetic in float64 (see the package doc and
// docs/graph-model.md): a path rate is the product of its legs. That is a
// deliberate choice, but it carries one failure mode that must never regress —
// a non-finite value entering the graph.
//
// NaN and +Inf are not caught by a `rate <= 0` guard, because every comparison
// with NaN is false. strconv.ParseFloat — which the ECB, Coinbase, Luno and BoC
// sources all use on strings taken straight off the wire — accepts the literal
// text "NaN", "Inf" and "infinity" with a nil error. A feed publishing one (BIS
// already publishes literal NaN for missing days) would therefore land a NaN
// rate in the graph, where it multiplies into every path that crosses the edge
// and then aborts encoding/json mid-response. Because api.writeJSON has already
// written a 200 and the Content-Type by then, and discards the encoder error,
// the consumer receives a 200 with a truncated or empty body — a silent
// corruption, which is the worst outcome for a client making correctness
// decisions on the numbers.
//
// These tests fail if that guard is weakened.

// Being finite is necessary but not sufficient: a rate must also be inside the
// magnitude band usable() enforces. 1e-306 and 1e300 are finite, positive and
// parse cleanly, yet a pair carrying both as direct quotes makes the accuracy
// model's (max-min)/min*10000 exactly +Inf — the same unencodable token, arrived
// at from two ordinary-looking numbers. These are the values that must never
// reach the graph.
func nonFinite() map[string]float64 {
	return map[string]float64{
		"NaN":      math.NaN(),
		"+Inf":     math.Inf(1),
		"-Inf":     math.Inf(-1),
		"zero":     0,
		"negative": -1.5,
		"denormal": math.SmallestNonzeroFloat64, // 1/x overflows to +Inf
		"maxfloat": math.MaxFloat64,             // finite, but out of band (1/x underflows)
		"tiny":     1e-306,                      // finite and positive, divides the spread to +Inf
		"huge":     1e300,                       // finite, but 1e300/1e-306 is not representable
	}
}

// TestNonFiniteEdgeNeverEntersGraph is the guard itself: no unusable rate may
// produce a reachable pair.
func TestNonFiniteEdgeNeverEntersGraph(t *testing.T) {
	now := time.Now().UTC()
	checked := 0
	for name, rate := range nonFinite() {
		checked++
		t.Run(name, func(t *testing.T) {
			g := NewGraph()
			g.Replace("bad", []Edge{{From: "USD", To: "ZAR", Rate: rate, Source: "bad", Time: now}})
			snap := g.Materialize(now)
			if p, ok := snap.Lookup("USD", "ZAR"); ok {
				t.Fatalf("rate %v (%s) was admitted to the graph as %v; it must be dropped", rate, name, p.Rate)
			}
			if len(snap.Currencies) != 0 {
				t.Fatalf("a dropped edge must not introduce currencies, got %v", snap.Currencies)
			}
		})
	}
	// Coverage: a future edit that empties nonFinite() would let this pass while
	// asserting nothing.
	if want := len(nonFinite()); checked != want || checked == 0 {
		t.Fatalf("exercised %d rejected rates, expected %d", checked, want)
	}
}

// TestPoisonedEdgeDoesNotBlankTheSnapshot is the consumer-visible consequence:
// one bad edge must not take the whole response down with it. The good pairs
// must still be present and the snapshot must still encode.
func TestPoisonedEdgeDoesNotBlankTheSnapshot(t *testing.T) {
	now := time.Now().UTC()
	for name, rate := range nonFinite() {
		t.Run(name, func(t *testing.T) {
			g := NewGraph()
			g.Replace("bad", []Edge{{From: "USD", To: "ZAR", Rate: rate, Source: "bad", Time: now}})
			g.Replace("good", []Edge{
				{From: "USD", To: "EUR", Rate: 0.92, Source: "good", Time: now},
				{From: "EUR", To: "GBP", Rate: 0.85, Source: "good", Time: now},
			})
			snap := g.Materialize(now)

			if _, ok := snap.Lookup("USD", "GBP"); !ok {
				t.Fatal("a bad edge must not remove unrelated reachable pairs")
			}
			assertSnapshotEncodes(t, snap)
		})
	}
}

// TestEveryMaterializedRateIsFinite walks every pair of a mixed graph and
// asserts the arithmetic never produced a non-finite number.
func TestEveryMaterializedRateIsFinite(t *testing.T) {
	now := time.Now().UTC()
	g := NewGraph()
	g.Replace("ecb", []Edge{
		{From: "EUR", To: "USD", Rate: 1.08, Source: "ecb", Time: now},
		{From: "EUR", To: "ZAR", Rate: 19.8, Source: "ecb", Time: now},
		{From: "EUR", To: "JPY", Rate: 168.0, Source: "ecb", Time: now},
	})
	g.Replace("coinbase", []Edge{
		{From: "USD", To: "ZAR", Rate: 16.44, Source: "coinbase", Time: now},
		{From: "USD", To: "BTC", Rate: 0.0000094, Source: "coinbase", Time: now},
	})
	g.Replace("junk", []Edge{
		{From: "USD", To: "XXX", Rate: math.NaN(), Source: "junk", Time: now},
		{From: "ZAR", To: "YYY", Rate: math.Inf(1), Source: "junk", Time: now},
	})
	snap := g.Materialize(now)

	checked := 0
	for _, from := range snap.Currencies {
		for _, to := range snap.Currencies {
			p, ok := snap.Lookup(from, to)
			if !ok {
				continue
			}
			checked++
			if !usable(p.Rate) {
				t.Fatalf("%s->%s materialized a non-usable rate %v", from, to, p.Rate)
			}
			for _, l := range p.Legs {
				if !usable(l.Rate) {
					t.Fatalf("%s->%s leg %s->%s has non-usable rate %v", from, to, l.From, l.To, l.Rate)
				}
			}
			for _, q := range snap.DirectQuotes(from, to) {
				if !usable(q.Rate) {
					t.Fatalf("%s->%s direct quote from %s is non-usable: %v", from, to, q.Source, q.Rate)
				}
			}
		}
	}
	// Assert coverage so a future change that silently empties the graph cannot
	// let this test pass by checking nothing.
	if want := 5 * 5; checked != want {
		t.Fatalf("expected %d reachable pairs across the 5 admitted currencies, checked %d", want, checked)
	}
	if len(snap.Currencies) != 5 {
		t.Fatalf("the two junk edges must contribute no currencies; got %v", snap.Currencies)
	}
	assertSnapshotEncodes(t, snap)
}

// TestInverseEdgeIsFiniteOrDropped covers the derived direction: the inverse
// edge is computed as 1/rate, so a rate that is itself fine can still produce a
// non-finite inverse.
func TestInverseEdgeIsFiniteOrDropped(t *testing.T) {
	now := time.Now().UTC()
	g := NewGraph()
	g.Replace("s", []Edge{{From: "A", To: "B", Rate: math.SmallestNonzeroFloat64, Source: "s", Time: now}})
	snap := g.Materialize(now)
	if _, ok := snap.Lookup("B", "A"); ok {
		t.Fatal("an edge whose inverse overflows to +Inf must be dropped in both directions")
	}
	if _, ok := snap.Lookup("A", "B"); ok {
		t.Fatal("an edge whose inverse overflows to +Inf must be dropped in both directions")
	}
}

// ─── The magnitude band ──────────────────────────────────────────────────────

// TestAdmittedRatesSpanEveryRealFeed guards the other direction: the band must
// not be so tight that it drops a rate somebody actually publishes. Each of
// these is a real quote shape named in usable()'s reasoning.
func TestAdmittedRatesSpanEveryRealFeed(t *testing.T) {
	real := map[string]float64{
		"USD->ZAR":                  18.5,
		"EUR->USD":                  1.08,
		"USD->BTC":                  0.0000094,
		"USD->satoshi":              940,
		"USD->IRR (managed)":        42000,
		"IRR->USD":                  1.0 / 42000,
		"old ZWD hyperinflation":    1e14,
		"inverse of old ZWD":        1e-14,
		"wei per ether (band edge)": 1e18,
		"ether per wei (band edge)": 1e-18,
	}
	for name, r := range real {
		if !usable(r) {
			t.Errorf("%s: rate %v must be admitted — the band has to clear every real feed", name, r)
		}
		// Closed under reciprocal: an edge implies its inverse, so admitting a
		// rate whose inverse is rejected would make admission one-directional.
		if !usable(1 / r) {
			t.Errorf("%s: rate %v is admitted but its inverse %v is not; the band must be closed under reciprocal", name, r, 1/r)
		}
	}
	if len(real) != 10 {
		t.Fatalf("this table pins 10 real-world rates, found %d", len(real))
	}
}

// TestBandEdgesAreAdmittedAndBeyondIsNot pins the boundary itself, in both
// directions, through the graph rather than through usable() alone.
func TestBandEdgesAreAdmittedAndBeyondIsNot(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name     string
		rate     float64
		admitted bool
	}{
		{"upper edge", 1e18, true},
		{"lower edge", 1e-18, true},
		{"just past the upper edge", 1e19, false},
		{"just past the lower edge", 1e-19, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGraph()
			g.Replace("s", []Edge{{From: "A", To: "B", Rate: tc.rate, Source: "s", Time: now}})
			snap := g.Materialize(now)
			_, fwd := snap.Lookup("A", "B")
			_, rev := snap.Lookup("B", "A")
			if fwd != tc.admitted || rev != tc.admitted {
				t.Fatalf("rate %v: admitted forward=%v reverse=%v, want %v in both directions",
					tc.rate, fwd, rev, tc.admitted)
			}
		})
	}
}

// TestPathProductStaysInBand covers the derived side of the gate: usable() is
// applied to each BFS product too, so a chain of individually-admissible legs
// cannot walk a pair out of the band.
func TestPathProductStaysInBand(t *testing.T) {
	now := time.Now().UTC()
	g := NewGraph()
	// Each leg is inside the band; A->D would be 1e30, which is not.
	g.Replace("s", []Edge{
		{From: "A", To: "B", Rate: 1e10, Source: "s", Time: now},
		{From: "B", To: "C", Rate: 1e10, Source: "s", Time: now},
		{From: "C", To: "D", Rate: 1e10, Source: "s", Time: now},
	})
	snap := g.Materialize(now)
	if p, ok := snap.Lookup("A", "D"); ok {
		t.Fatalf("A->D materialized %v, which is outside the band; a derived rate must be gated too", p.Rate)
	}
	// The two-hop prefix is 1e20, also out of band.
	if p, ok := snap.Lookup("A", "C"); ok {
		t.Fatalf("A->C materialized %v, which is outside the band", p.Rate)
	}
	// And the legs themselves still work.
	if _, ok := snap.Lookup("A", "B"); !ok {
		t.Fatal("A->B is a single in-band leg and must be reachable")
	}
}

// ─── Assess must always encode ───────────────────────────────────────────────

// TestAssessEncodesForAdversarialQuotes drives fx.Assess with the quote shapes
// that used to produce a non-finite Assessment. Each row names the expression
// that overflowed: the assertion is that no combination of admissible-looking
// quotes can put a token json.Marshal refuses into a response.
func TestAssessEncodesForAdversarialQuotes(t *testing.T) {
	now := time.Now().UTC()
	p := Pair{Rate: 18.5, Hops: 1, AsOf: now, Sources: []string{"sarb"}}

	cases := []struct {
		name   string
		quotes []Quote
	}{
		{"denormal beside a real rate — (max-min)/min overflows", []Quote{
			{Source: "a", Rate: 16.5, Time: now}, {Source: "b", Rate: 1e-306, Time: now}}},
		{"opposite extremes — (max-min)/min overflows", []Quote{
			{Source: "a", Rate: 1e-306, Time: now}, {Source: "b", Rate: 1e300, Time: now}}},
		{"two maxfloats — the sum, and so mean and stdev, overflow", []Quote{
			{Source: "a", Rate: math.MaxFloat64, Time: now}, {Source: "b", Rate: math.MaxFloat64, Time: now}}},
		{"spread finite but round2's f*100 overflows", []Quote{
			{Source: "a", Rate: 1e-300, Time: now}, {Source: "b", Rate: 1e4, Time: now}}},
		{"+Inf quote — Inf > 0 passes a positivity check", []Quote{
			{Source: "a", Rate: math.Inf(1), Time: now}, {Source: "b", Rate: 18.5, Time: now}}},
		{"NaN quote", []Quote{
			{Source: "a", Rate: math.NaN(), Time: now}, {Source: "b", Rate: 18.5, Time: now}}},
		{"every quote out of band", []Quote{
			{Source: "a", Rate: 1e300, Time: now}, {Source: "b", Rate: 1e301, Time: now}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := Assess("USD", "ZAR", p, tc.quotes, now)
			assertAssessmentEncodes(t, a)
			// An out-of-band quote is dropped, never counted as a corroborating
			// source: reporting it would claim agreement we cannot measure.
			for _, q := range tc.quotes {
				if !usable(q.Rate) && a.Corroboration.Sources > 1 {
					t.Errorf("quote %v is out of band but corroboration counts %d sources: %+v",
						q.Rate, a.Corroboration.Sources, a.Corroboration)
				}
			}
		})
	}
	if len(cases) != 7 {
		t.Fatalf("this table pins 7 adversarial quote shapes, found %d", len(cases))
	}
}

// TestAssessEncodesOnAMaterializedGraph is the end-to-end shape: build a graph
// the way a refresh does, take the pair and its direct quotes straight out of
// the snapshot, and assess it. Nothing here reaches past the public API, which
// is why this is the version that matters — the quotes are whatever the graph
// chose to keep, not a hand-made slice.
func TestAssessEncodesOnAMaterializedGraph(t *testing.T) {
	now := time.Now().UTC()
	g := NewGraph()
	g.Replace("sarb", []Edge{{From: "USD", To: "ZAR", Rate: 18.5, Source: "sarb", Time: now}})
	// Two more sources quoting the same pair with numbers that are positive and
	// finite but not rates. Before the band existed both were admitted, and the
	// resulting spread of (1e300-1e-306)/1e-306*10000 was +Inf.
	g.Replace("junk-low", []Edge{{From: "USD", To: "ZAR", Rate: 1e-306, Source: "junk-low", Time: now}})
	g.Replace("junk-high", []Edge{{From: "USD", To: "ZAR", Rate: 1e300, Source: "junk-high", Time: now}})

	snap := g.Materialize(now)
	p, ok := snap.Lookup("USD", "ZAR")
	if !ok {
		t.Fatal("the good sarb edge must still make USD->ZAR reachable")
	}
	quotes := snap.DirectQuotes("USD", "ZAR")
	if len(quotes) != 1 || quotes[0].Source != "sarb" {
		t.Fatalf("only the in-band quote may survive, got %+v", quotes)
	}
	a := Assess("USD", "ZAR", p, quotes, now)
	assertAssessmentEncodes(t, a)
	if a.Corroboration.Sources != 1 {
		t.Errorf("corroboration = %d sources, want 1 (the two junk quotes are not corroboration)", a.Corroboration.Sources)
	}
	if a.Corroboration.Agree {
		t.Error("a single surviving quote must not report agreement")
	}
	// The whole snapshot must still encode, not just the assessment.
	assertSnapshotEncodes(t, snap)
}

// ─── The arithmetic itself ───────────────────────────────────────────────────

// TestRound2DoesNotOverflow pins the belt-and-braces half: round2 is reachable
// from anywhere in the package, and math.Round(f*100)/100 turns any finite
// |f| >= ~1.8e306 into ±Inf.
func TestRound2DoesNotOverflow(t *testing.T) {
	for _, f := range []float64{1e307, 1.5e308, math.MaxFloat64, -1e307, -math.MaxFloat64} {
		got := round2(f)
		if math.IsInf(got, 0) || math.IsNaN(got) {
			t.Errorf("round2(%v) = %v; a finite input must not come back non-finite", f, got)
		}
		// Above 2^53/100 a float64 has no fractional part left, so the correct
		// two-decimal rounding of these inputs is the input.
		if got != f {
			t.Errorf("round2(%v) = %v, want the input unchanged", f, got)
		}
	}
	// The common path must be untouched, bit for bit.
	for _, tc := range []struct{ in, want float64 }{
		{0.7776, 0.78}, {0.125, 0.13}, {0.999, 1.0}, {0, 0}, {29.4449, 29.44}, {1e14, 1e14},
	} {
		if got := round2(tc.in); got != tc.want {
			t.Errorf("round2(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestBoundedBpsClampsToTheWorstBucket pins what the clamp is allowed to do. A
// dispersion we could not compute must never read as a small one.
func TestBoundedBpsClampsToTheWorstBucket(t *testing.T) {
	for _, bad := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		got := boundedBps(bad)
		if math.IsInf(got, 0) || math.IsNaN(got) {
			t.Fatalf("boundedBps(%v) = %v, which the JSON encoder still refuses", bad, got)
		}
		if b, err := json.Marshal(Corroboration{Sources: 2, SpreadBps: got, StdevBps: got}); err != nil {
			t.Fatalf("clamped dispersion %v must be encodable: %v", got, err)
		} else if strings.Contains(string(b), "Inf") || strings.Contains(string(b), "NaN") {
			t.Fatalf("clamped dispersion encoded as %s", b)
		}
		factor, agree := spreadBand(got)
		if agree {
			t.Errorf("boundedBps(%v) -> %v reports agreement; an uncomputable spread is not agreement", bad, got)
		}
		if factor != 0.72 {
			t.Errorf("boundedBps(%v) -> %v earns factor %v, want the worst bucket 0.72", bad, got, factor)
		}
	}
	// Finite input passes through untouched, so the common path is unaffected.
	for _, ok := range []float64{0, 10.5, 25, 300, 1e40} {
		if got := boundedBps(ok); got != ok {
			t.Errorf("boundedBps(%v) = %v, want it unchanged", ok, got)
		}
	}
}

func assertAssessmentEncodes(t *testing.T, a Assessment) {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("assessment must always be JSON-encodable, got: %v (%+v)", err, a)
	}
	// Same encoder api.writeJSON streams through: if these tokens can appear at
	// all the response is already corrupt.
	for _, bad := range []string{"NaN", "Inf"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("encoded assessment contains %q: %s", bad, b)
		}
	}
}

func assertSnapshotEncodes(t *testing.T, snap *Snapshot) {
	t.Helper()
	rows := map[string]map[string]Pair{}
	for _, from := range snap.Currencies {
		row := map[string]Pair{}
		for _, to := range snap.Currencies {
			if p, ok := snap.Lookup(from, to); ok {
				row[to] = p
			}
		}
		rows[from] = row
	}
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("snapshot must always be JSON-encodable, got: %v", err)
	}
	// json.Marshal is the same encoder api.writeJSON streams through; if it can
	// emit these tokens at all the response is already corrupt.
	for _, bad := range []string{"NaN", "Inf"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("encoded snapshot contains %q: %s", bad, b)
		}
	}
}
