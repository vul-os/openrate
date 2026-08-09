package fx

import (
	"math"
	"testing"
)

// Two invariants live in this file, and they are not the same invariant. Confusing
// them is what put a false claim on the landing page.
//
//  1. AT FULL PRECISION the product of a pair's legs IS Pair.Rate — bit for bit,
//     not "to within an epsilon". Materialize accumulates the path rate by the
//     same left-to-right float64 multiplication, in the same order the legs are
//     appended, so replaying the legs reproduces the identical double. This is
//     the invariant Leg's doc comment states, the one the API's `legs` array
//     exists to let a consumer check, and it holds for every pair.
//
//  2. AT DISPLAY PRECISION it does not hold, and cannot. Every number a reader
//     sees is rounded independently — each leg once, the rate once — and three
//     roundings do not compose. Multiplying the six-decimal legs printed in the
//     UI reproduces the six-decimal rate only to within that rounding; on real
//     rates the last digit differs on most multi-hop pairs. What IS true, for
//     every pair, is a bound: the gap between the two displayed numbers is no
//     wider than the rounding of the inputs can make it. That bound is what the
//     site and the web UI now assert, so this file pins it.
//
// The fixture is a real Coinbase snapshot (USD base, 31 Jul 2026), taken from a
// live run of the binary, because the whole point is that awkward real rates —
// not tidy test constants — are where the display arithmetic parts company.

// displayDigits is the precision the web UI and the landing page print rates at
// (web/ui.html: fmt(x, 6)).
const displayDigits = 6

// displayUnit is one unit in the last displayed place, and displayHalf is the
// most a single round-to-6dp can move a number.
const (
	displayUnit = 1e-6
	displayHalf = displayUnit / 2
)

// round6 rounds half away from zero, matching Intl.NumberFormat's default
// rounding mode ("halfExpand") — the one the browser applies to every number
// web/ui.html prints.
func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}

// coinbaseUSD is the USD-base file as the running binary saw it. Every rate is
// the exact float64 the API returned; ZAR is here so the ZAR→USD inverse edge
// (0.06025387367132677) is the one the engine actually derives.
var coinbaseUSD = map[string]float64{
	"ZAR": 16.596443333333333,
	"AED": 3.672553333333333,
	"AUD": 1.4261563333333334,
	"CZK": 21.105399,
	"IDR": 18062.7293655,
	"KRW": 1437.5218575,
	"EUR": 0.8563577333333333,
}

// fixtureGraph builds the snapshot the two tests below share.
func fixtureGraph(t *testing.T) *Snapshot {
	t.Helper()
	var edges []Edge
	for ccy, rate := range coinbaseUSD {
		edges = append(edges, e("USD", ccy, rate, "coinbase", tBase))
	}
	g := NewGraph()
	g.Replace("coinbase", edges)
	snap := g.Materialize(tBase)
	if len(snap.Currencies) != len(coinbaseUSD)+1 {
		t.Fatalf("fixture built %d currencies, want %d — the fixture is broken and these tests check nothing",
			len(snap.Currencies), len(coinbaseUSD)+1)
	}
	return snap
}

// TestLegsMultiplyToPairRateExactly pins invariant (1): the documented contract
// on Leg. Replaying the legs left to right reproduces Pair.Rate as the SAME
// float64, with no tolerance at all. A consumer that recomputes a cross rate
// from the `legs` array gets the engine's own number back.
//
// It also pins the shape the legs must have for that replay to be meaningful:
// one leg per hop, chained head to tail along `path`.
func TestLegsMultiplyToPairRateExactly(t *testing.T) {
	snap := fixtureGraph(t)

	checked, multiHop := 0, 0
	for _, from := range snap.Currencies {
		for _, to := range snap.Currencies {
			if from == to {
				continue
			}
			p, ok := snap.Lookup(from, to)
			if !ok {
				t.Fatalf("%s→%s unreachable in a fully connected fixture", from, to)
			}
			checked++
			if p.Hops > 1 {
				multiHop++
			}

			if len(p.Legs) != p.Hops {
				t.Errorf("%s→%s: %d legs for %d hops — the calculation cannot be replayed", from, to, len(p.Legs), p.Hops)
				continue
			}
			if len(p.Path) != p.Hops+1 {
				t.Errorf("%s→%s: path %v does not have %d+1 nodes", from, to, p.Path, p.Hops)
				continue
			}

			product := 1.0
			for i, l := range p.Legs {
				if l.From != p.Path[i] || l.To != p.Path[i+1] {
					t.Errorf("%s→%s: leg %d is %s→%s but path step %d is %s→%s — the legs do not walk the path",
						from, to, i, l.From, l.To, i, p.Path[i], p.Path[i+1])
				}
				product *= l.Rate
			}
			if product != p.Rate {
				t.Errorf("%s→%s: legs multiply to %v but Pair.Rate is %v — Leg's documented invariant "+
					"(the product of all legs' rates IS Pair.Rate, exactly) is broken",
					from, to, product, p.Rate)
			}
		}
	}

	// Floors: an exactness check that only ever saw direct pairs would prove
	// nothing, since a 1-hop pair's single leg is trivially its own rate.
	if checked < 40 || multiHop < 20 {
		t.Fatalf("checked %d pairs of which %d were multi-hop — too few to have tested the invariant that matters", checked, multiHop)
	}
	t.Logf("%d pairs (%d multi-hop): legs replay to Pair.Rate bit-for-bit", checked, multiHop)
}

// TestDisplayedLegsReconcileOnlyWithinDisplayRounding pins invariant (2).
//
// It asserts three things, and the third is the one that stops the false claim
// coming back:
//
//   - the bound holds for every pair: the displayed product and the displayed
//     rate differ by no more than the rounding of the inputs allows;
//   - the bound is not vacuous — it is derived from the leg magnitudes, so a
//     0.06 leg rounded to six decimals is allowed ~2 parts per million and a
//     leg near 1 is allowed far less;
//   - on real rates the displayed numbers DO part company. If this stops being
//     true the fixture has drifted to flattering values, and any page that says
//     "the displayed legs multiply out exactly" is relying on luck.
func TestDisplayedLegsReconcileOnlyWithinDisplayRounding(t *testing.T) {
	snap := fixtureGraph(t)

	checked, mismatched := 0, 0
	var worst float64
	var worstPair string
	for _, from := range snap.Currencies {
		for _, to := range snap.Currencies {
			if from == to {
				continue
			}
			p, _ := snap.Lookup(from, to)
			if p.Hops < 2 {
				continue // a direct pair's leg IS the rate; rounding both is the same rounding
			}
			checked++

			// What a reader multiplies: the legs as printed.
			shownProduct := 1.0
			for _, l := range p.Legs {
				shownProduct *= round6(l.Rate)
			}
			shownProduct = round6(shownProduct)
			shownRate := round6(p.Rate)

			// The bound. Each displayed leg lies within displayHalf of the true
			// leg, so the true product lies between the product of the low ends
			// and the product of the high ends. Rounding the product for display,
			// and rounding the rate for display, can each move a number by
			// another displayHalf.
			lo, hi := 1.0, 1.0
			for _, l := range p.Legs {
				lo *= math.Max(0, l.Rate-displayHalf)
				hi *= l.Rate + displayHalf
			}
			bound := (hi - lo) + displayUnit
			bound = bound*(1+1e-9) + 1e-12 // slack for the float error in computing the bound itself

			diff := math.Abs(shownProduct - shownRate)
			if diff > bound {
				t.Errorf("%s→%s: displayed legs multiply to %.6f but the displayed rate is %.6f — a gap of %g, "+
					"wider than display rounding can explain (bound %g). Something other than rounding is wrong.",
					from, to, shownProduct, shownRate, diff, bound)
			}
			if diff > 0 {
				mismatched++
				if rel := diff / shownRate; rel > worst {
					worst, worstPair = rel, from+"→"+to
				}
			}
		}
	}

	if checked < 20 {
		t.Fatalf("only %d multi-hop pairs examined — this test verified almost nothing", checked)
	}
	if mismatched == 0 {
		t.Fatalf("not one of %d multi-hop pairs showed a display-rounding residual. Real rates do: this fixture "+
			"has drifted to values where the rounding happens to land, which is exactly the trap that put "+
			"\"the displayed legs multiply out exactly at six decimal places\" on the landing page. Restore a "+
			"realistic fixture rather than relaxing this assertion.", checked)
	}
	t.Logf("%d multi-hop pairs: %d differ at %d displayed decimals, worst %s at %.2g relative — every one inside the rounding bound",
		checked, mismatched, displayDigits, worstPair, worst)
}
