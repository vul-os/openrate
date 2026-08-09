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

func nonFinite() map[string]float64 {
	return map[string]float64{
		"NaN":      math.NaN(),
		"+Inf":     math.Inf(1),
		"-Inf":     math.Inf(-1),
		"zero":     0,
		"negative": -1.5,
		"denormal": math.SmallestNonzeroFloat64, // 1/x overflows to +Inf
		"maxfloat": math.MaxFloat64,             // finite, but 1/x underflows to 0
	}
}

// TestNonFiniteEdgeNeverEntersGraph is the guard itself: no unusable rate may
// produce a reachable pair.
func TestNonFiniteEdgeNeverEntersGraph(t *testing.T) {
	now := time.Now().UTC()
	for name, rate := range nonFinite() {
		if name == "maxfloat" {
			continue // finite and positive: admitted, covered below
		}
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
