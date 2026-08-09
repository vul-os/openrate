package fx

import (
	"errors"
	"fmt"
	"math"
	"testing"
)

// TestDescribeCarriesTheRateThroughUntouched is the facade half of
// precision_test.go's invariant (1). That test pins "the product of the legs IS
// Pair.Rate, bit for bit" on the snapshot. This one pins that the value handed
// to a caller is still that same double: Describe must copy the rate and the
// legs, never re-derive them. One stray multiply-then-divide, one round on the
// way out, and the `legs` array in the API response stops reconciling with the
// `rate` beside it — which is precisely the claim openrate makes.
func TestDescribeCarriesTheRateThroughUntouched(t *testing.T) {
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
			c, err := Describe(snap, from, to, 1, tBase)
			if err != nil {
				t.Fatalf("Describe(%s→%s): %v", from, to, err)
			}
			checked++
			if p.Hops > 1 {
				multiHop++
			}

			if c.Rate != p.Rate {
				t.Errorf("%s→%s: Conversion.Rate is %v but Pair.Rate is %v — the facade perturbed the rate",
					from, to, c.Rate, p.Rate)
			}
			if len(c.Legs) != len(p.Legs) {
				t.Fatalf("%s→%s: %d legs through Describe, %d in the pair", from, to, len(c.Legs), len(p.Legs))
			}
			product := 1.0
			for i, l := range c.Legs {
				if l.Rate != p.Legs[i].Rate {
					t.Errorf("%s→%s: leg %d is %v through Describe, %v in the pair", from, to, i, l.Rate, p.Legs[i].Rate)
				}
				product *= l.Rate
			}
			if product != c.Rate {
				t.Errorf("%s→%s: the legs Describe returned multiply to %v, but the rate it returned is %v",
					from, to, product, c.Rate)
			}
			// Amount 1 must not cost precision either: 1 × rate is exact in
			// IEEE-754, so the result IS the rate.
			if c.Result != p.Rate {
				t.Errorf("%s→%s: 1 unit converts to %v, want the rate itself %v", from, to, c.Result, p.Rate)
			}
		}
	}
	if checked < 40 || multiHop < 20 {
		t.Fatalf("checked %d pairs of which %d were multi-hop — too few to have tested the invariant that matters", checked, multiHop)
	}
	t.Logf("%d pairs (%d multi-hop) survive Describe bit-for-bit", checked, multiHop)
}

// TestDescribeMutationCannotReachTheSnapshot: the snapshot is shared across
// goroutines and treated as immutable everywhere. A caller writing to the slices
// it got back must not be able to rewrite the engine's own answer.
func TestDescribeMutationCannotReachTheSnapshot(t *testing.T) {
	snap := fixtureGraph(t)
	c, err := Describe(snap, "ZAR", "EUR", 1, tBase)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if len(c.Legs) == 0 || len(c.Path) == 0 {
		t.Fatal("fixture pair has no legs/path; this test would check nothing")
	}
	c.Legs[0].Rate = 999
	c.Path[0] = "XXX"

	again, err := Describe(snap, "ZAR", "EUR", 1, tBase)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if again.Legs[0].Rate == 999 || again.Path[0] == "XXX" {
		t.Fatal("writing to a returned Conversion changed the snapshot behind it")
	}
}

func TestDescribeUnknownPair(t *testing.T) {
	snap := fixtureGraph(t)
	if _, err := Describe(snap, "USD", "XXX", 1, tBase); !errors.Is(err, ErrUnknownPair) {
		t.Fatalf("unknown pair: err = %v, want ErrUnknownPair", err)
	}
}

func TestDescribeNonFiniteAmount(t *testing.T) {
	snap := fixtureGraph(t)
	for name, amt := range map[string]float64{"NaN": math.NaN(), "+Inf": math.Inf(1), "-Inf": math.Inf(-1)} {
		if _, err := Describe(snap, "USD", "ZAR", amt, tBase); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("amount %s: err = %v, want ErrInvalidAmount", name, err)
		}
	}
}

// A finite amount and a finite rate can still have no representable product.
func TestDescribeOverflowingProduct(t *testing.T) {
	snap := fixtureGraph(t)
	if _, err := Describe(snap, "USD", "ZAR", math.MaxFloat64, tBase); !errors.Is(err, ErrAmountOutOfRange) {
		t.Fatalf("overflowing product: err = %v, want ErrAmountOutOfRange", err)
	}
	// The guard must not have broken ordinary conversion.
	c, err := Describe(snap, "USD", "ZAR", 100, tBase)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if want := 100 * coinbaseUSD["ZAR"]; c.Result != want {
		t.Fatalf("100 USD→ZAR = %v, want %v", c.Result, want)
	}
}

// Self-conversion is the identity, and it must not need an edge to exist.
func TestDescribeSelfPair(t *testing.T) {
	snap := fixtureGraph(t)
	c, err := Describe(snap, "USD", "USD", 42, tBase)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if c.Rate != 1 || c.Result != 42 {
		t.Fatalf("USD→USD 42: rate %v result %v, want 1 and 42", c.Rate, c.Result)
	}
}

// Describe must attach the grade, not leave the zero Assessment behind: the
// grade is the reason the provenance is carried at all.
func TestDescribeAttachesQuality(t *testing.T) {
	snap := fixtureGraph(t)
	c, err := Describe(snap, "USD", "ZAR", 1, tBase)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	want := Assess("USD", "ZAR", mustLookup(t, snap, "USD", "ZAR"), snap.DirectQuotes("USD", "ZAR"), tBase)
	if fmt.Sprintf("%+v", c.Quality) != fmt.Sprintf("%+v", want) {
		t.Fatalf("Quality = %+v, want %+v", c.Quality, want)
	}
	if c.Quality.Grade == "" {
		t.Fatal("Quality.Grade is empty — the assessment was never made")
	}
}

func mustLookup(t *testing.T, snap *Snapshot, from, to string) Pair {
	t.Helper()
	p, ok := snap.Lookup(from, to)
	if !ok {
		t.Fatalf("%s→%s unreachable", from, to)
	}
	return p
}
