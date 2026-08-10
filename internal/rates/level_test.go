package rates

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// The admission band on a published level, tested where the data enters.
//
// The bound used to live only in internal/ratequality, which meant the model
// itself would admit any finite value and a published Series.Value could be
// 1e300. That encodes, so it was never a fault a user saw — and that is exactly
// what made it worth moving. An invariant enforced one layer downstream holds
// only for the callers that happen to go through that layer, and rates.Series
// is handed to serve, to the interest API and to anything that materializes a
// snapshot, none of which grade it first.

// TestMaterializeRefusesALevelOutsideTheBand walks every shape that used to get
// through: absurd-but-finite in both directions, and the two tokens the JSON
// encoder refuses outright.
func TestMaterializeRefusesALevelOutsideTheBand(t *testing.T) {
	cases := []struct {
		name  string
		value float64
	}{
		{"a mis-scaled level", 1e300},
		{"the same, negative", -1e300},
		{"just past the band", 1.0000001e12},
		{"just past the band, negative", -1.0000001e12},
		{"the largest float there is", math.MaxFloat64},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
		{"NaN, which BIS emits for a missing day", math.NaN()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := New()
			// One good source and one bad, on the same series, so the series
			// survives and the question is only whether the bad value is in it.
			b.Replace("bis", []Observation{
				obs("za.policy", "ZA", TypePolicy, 7.25, rNow, "bis"),
			})
			b.Replace("junk", []Observation{
				obs("za.policy", "ZA", TypePolicy, tc.value, rNow, "junk"),
			})
			snap := b.Materialize(rNow)

			s, ok := snap.Lookup("za.policy")
			if !ok {
				t.Fatal("za.policy is missing; the good observation should have kept it alive")
			}
			if s.Value != 7.25 || s.Source != "bis" {
				t.Errorf("headline is %v from %q, want 7.25 from bis — the out-of-band value won the "+
					"series", s.Value, s.Source)
			}
			for _, src := range s.Sources {
				if src == "junk" {
					t.Errorf("%v was admitted as a source's contribution; a level outside the band is a "+
						"parse or scaling error, not a rate", tc.value)
				}
			}
			for _, q := range s.Latest {
				if q.Source == "junk" {
					t.Errorf("%v was counted as corroboration at %v; a value nothing can measure "+
						"against is not agreement", tc.value, q.Value)
				}
			}
			for _, p := range s.History {
				if !UsableLevel(p.Value) {
					t.Errorf("history carries %v, which is outside the band", p.Value)
				}
			}
		})
	}
	if len(cases) != 8 {
		t.Fatalf("this table pins 8 out-of-band shapes, found %d", len(cases))
	}
}

// TestMaterializeDropsASeriesWithOnlyBadLevels is the other end of the same
// rule: a series whose every observation is out of band must not be published
// as an empty husk with an invented headline.
func TestMaterializeDropsASeriesWithOnlyBadLevels(t *testing.T) {
	b := New()
	b.Replace("junk", []Observation{
		obs("xx.policy", "XX", TypePolicy, 1e300, rNow, "junk"),
		obs("xx.policy", "XX", TypePolicy, math.NaN(), rNow.AddDate(0, 0, -1), "junk"),
	})
	b.Replace("bis", []Observation{
		obs("za.policy", "ZA", TypePolicy, 7.25, rNow, "bis"),
	})
	snap := b.Materialize(rNow)

	if _, ok := snap.Lookup("xx.policy"); ok {
		t.Error("xx.policy was published from nothing but out-of-band values")
	}
	for _, id := range snap.IDs() {
		if id == "xx.policy" {
			t.Error("xx.policy is listed in IDs() but has no admissible observation behind it")
		}
	}
	// The control: the sound series is still there, so "dropped" is not "the
	// book came out empty".
	if _, ok := snap.Lookup("za.policy"); !ok {
		t.Fatal("za.policy went missing too; the guard is dropping everything")
	}
	if len(snap.IDs()) != 1 {
		t.Errorf("snapshot holds %v, want just za.policy", snap.IDs())
	}
}

// TestMaterializeAdmitsEveryRealSeries is the direction that keeps the bound
// honest. A guard that refuses everything is not a guard, and the upper end has
// to clear an index level, which is not a percentage and carries many digits.
func TestMaterializeAdmitsEveryRealSeries(t *testing.T) {
	real := map[string]float64{
		"SARB repo":                   7.25,
		"US policy midpoint":          3.625,
		"ECB deposit rate, negative":  -0.5,
		"Swiss policy rate, negative": -0.75,
		"Argentine policy rate":       133.0,
		"Zimbabwe policy rate":        200.0,
		"a ZARONIA index level":       123.4,
		"a rebased index level":       1.5e9,
		"exactly the upper edge":      MaxLevel,
		"exactly the lower edge":      -MaxLevel,
		"zero":                        0,
	}
	b := New()
	for name, v := range real {
		id := "x" + strings.ToLower(strings.ReplaceAll(strings.Fields(name)[0], ",", "")) + ".policy"
		b.Replace(name, []Observation{obs(id, "XX", TypePolicy, v, rNow, name)})
	}
	snap := b.Materialize(rNow)

	admitted := 0
	for _, id := range snap.IDs() {
		s, _ := snap.Lookup(id)
		admitted += len(s.Sources)
	}
	if admitted != len(real) {
		t.Errorf("admitted %d of %d real-world levels; the band has to clear every series a feed "+
			"actually publishes: %v", admitted, len(real), real)
	}
	if len(real) != 11 {
		t.Fatalf("this table pins 11 real-world levels, found %d", len(real))
	}
}

// TestAPublishedSeriesAlwaysEncodes is the consequence, stated as the thing a
// consumer sees. serve encodes a response into a buffer before writing the
// status line, so a value the encoder refuses is a 500 rather than a bad field.
func TestAPublishedSeriesAlwaysEncodes(t *testing.T) {
	b := New()
	b.Replace("bis", []Observation{
		obs("za.policy", "ZA", TypePolicy, 7.25, rNow, "bis"),
		obs("us.policy", "US", TypePolicy, 3.625, rNow, "bis"),
	})
	b.Replace("junk", []Observation{
		obs("za.policy", "ZA", TypePolicy, math.Inf(1), rNow, "junk"),
		obs("us.policy", "US", TypePolicy, 1e300, rNow, "junk"),
	})
	snap := b.Materialize(rNow)

	checked := 0
	for _, id := range snap.IDs() {
		s, ok := snap.Lookup(id)
		if !ok {
			continue
		}
		checked++
		enc, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("%s does not encode: %v (%+v)", id, err, s)
		}
		for _, bad := range []string{"NaN", "Inf", "e+300", "e+308"} {
			if strings.Contains(string(enc), bad) {
				t.Errorf("%s encodes with %q in it: %s", id, bad, enc)
			}
		}
	}
	if checked != 2 {
		t.Fatalf("encoded %d series, expected 2 — a book that came out empty would satisfy every "+
			"assertion above", checked)
	}
}

// TestUsableLevelIsTheOnePredicate pins the boundary itself, so a change to the
// constant is a deliberate one and not a rounding accident.
func TestUsableLevelIsTheOnePredicate(t *testing.T) {
	if MaxLevel != 1e12 {
		t.Errorf("MaxLevel is %v; the published band is [-1e12, 1e12] and internal/ratequality's "+
			"overflow argument is written against that number", MaxLevel)
	}
	for _, v := range []float64{0, 1, -1, 7.25, -0.5, 1.5e9, MaxLevel, -MaxLevel} {
		if !UsableLevel(v) {
			t.Errorf("UsableLevel(%v) = false, want true", v)
		}
	}
	for _, v := range []float64{
		math.Nextafter(MaxLevel, math.Inf(1)),
		math.Nextafter(-MaxLevel, math.Inf(-1)),
		1e13, -1e13, 1e300, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN(),
	} {
		if UsableLevel(v) {
			t.Errorf("UsableLevel(%v) = true, want false", v)
		}
	}
}
