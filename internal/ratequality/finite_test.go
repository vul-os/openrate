package ratequality

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/vul-os/openrate/internal/rates"
)

// The interest-rate side of the same hazard the FX package guards (see
// fx/finite_test.go). The arithmetic differs — dispersion here is the absolute
// (max-min)*100, not a ratio — but the failure is identical: a level that is
// finite yet absurd turns a Corroboration field into ±Inf or NaN, and
// encoding/json refuses to emit either. Since serve encodes into a buffer before
// writing, the consumer-visible result is a 500 on a series whose only problem
// is one nonsense value from one source; before that fix it was a 200 with an
// empty body.
//
// rates.Materialize already drops a non-finite observation, but Assess takes a
// rates.Series directly and nothing about its type says the values are sane, so
// the guard belongs here too.

var finNow = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

func quoted(values ...float64) rates.Series {
	s := rates.Series{
		Series: "xx.policy", Area: "XX", Type: rates.TypePolicy,
		Value: values[0], Date: finNow, Source: "bis",
	}
	for i, v := range values {
		s.Latest = append(s.Latest, rates.Quote{Source: string(rune('a' + i)), Value: v, Date: finNow})
	}
	return s
}

// TestAssessEncodesForAdversarialLevels drives Assess through every level shape
// that used to produce a non-finite Assessment.
func TestAssessEncodesForAdversarialLevels(t *testing.T) {
	cases := []struct {
		name   string
		values []float64
	}{
		{"opposite float extremes — (max-min)*100 overflows", []float64{-math.MaxFloat64, math.MaxFloat64}},
		{"two huge levels — the sum, and so the mean, overflows", []float64{math.MaxFloat64, math.MaxFloat64 / 2}},
		{"round2's f*100 overflows on the mean", []float64{1e307, 1e307}},
		{"+Inf level", []float64{math.Inf(1), 7.25}},
		{"-Inf level", []float64{math.Inf(-1), 7.25}},
		{"NaN level", []float64{math.NaN(), 7.25}},
		{"one real level, one absurd", []float64{7.25, 1e300}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := Assess(quoted(tc.values...), finNow)
			assertAssessmentEncodes(t, a)
			// An unusable level is dropped, never counted as a corroborating
			// source: counting it would claim agreement we cannot measure.
			for _, v := range tc.values {
				if !usableLevel(v) && a.Corroboration.Sources > 1 {
					t.Errorf("level %v is out of band but corroboration counts %d sources: %+v",
						v, a.Corroboration.Sources, a.Corroboration)
				}
			}
		})
	}
	if len(cases) != 7 {
		t.Fatalf("this table pins 7 adversarial level shapes, found %d", len(cases))
	}
}

// TestUsableLevelAdmitsEveryRealSeries guards the other direction: the band must
// not drop a level a feed actually publishes. Negative policy rates are ordinary
// and index levels are the reason the upper bound is generous.
func TestUsableLevelAdmitsEveryRealSeries(t *testing.T) {
	real := map[string]float64{
		"SARB repo":                   7.25,
		"US policy midpoint":          3.625,
		"ECB deposit rate, negative":  -0.5,
		"Swiss policy rate, negative": -0.75,
		"Argentine policy rate":       133.0,
		"Zimbabwe policy rate":        200.0,
		"an index level":              123.4,
		"a rebased index level":       1.5e9,
		"upper band edge":             1e12,
		"lower band edge":             -1e12,
	}
	for name, v := range real {
		if !usableLevel(v) {
			t.Errorf("%s: level %v must be admitted — the band has to clear every real series", name, v)
		}
	}
	for _, v := range []float64{1e13, -1e13, math.MaxFloat64, math.Inf(1), math.Inf(-1), math.NaN()} {
		if usableLevel(v) {
			t.Errorf("level %v is outside the band and must be rejected", v)
		}
	}
	if len(real) != 10 {
		t.Fatalf("this table pins 10 real-world levels, found %d", len(real))
	}
}

// TestRound2DoesNotOverflow pins the belt-and-braces half: round2 is applied to
// both the confidence and the mean, and math.Round(f*100)/100 turns any finite
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
		{0.123456, 0.12}, {0.125, 0.13}, {0.999, 1.0}, {7.05, 7.05}, {0, 0},
	} {
		if got := round2(tc.in); got != tc.want {
			t.Errorf("round2(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestBoundedBpsClampsToTheWorstBucket pins what the clamp may do: a dispersion
// we could not compute must never read as a small one.
func TestBoundedBpsClampsToTheWorstBucket(t *testing.T) {
	for _, bad := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		got := boundedBps(bad)
		if math.IsInf(got, 0) || math.IsNaN(got) {
			t.Fatalf("boundedBps(%v) = %v, which the JSON encoder still refuses", bad, got)
		}
		b, err := json.Marshal(Corroboration{Sources: 2, SpreadBps: got})
		if err != nil {
			t.Fatalf("clamped dispersion %v must be encodable: %v", got, err)
		}
		if strings.Contains(string(b), "Inf") || strings.Contains(string(b), "NaN") {
			t.Fatalf("clamped dispersion encoded as %s", b)
		}
		factor, agree := spreadBand(got)
		if agree {
			t.Errorf("boundedBps(%v) -> %v reports agreement; an uncomputable spread is not agreement", bad, got)
		}
		if factor != 0.78 {
			t.Errorf("boundedBps(%v) -> %v earns factor %v, want the worst bucket 0.78", bad, got, factor)
		}
	}
	for _, ok := range []float64{0, 1, 5, 25, 2e14} {
		if got := boundedBps(ok); got != ok {
			t.Errorf("boundedBps(%v) = %v, want it unchanged", ok, got)
		}
	}
}

// TestEveryCorroborationFieldIsFinite walks a mixed book the way the API does and
// asserts no field of any Assessment is a token the encoder rejects.
func TestEveryCorroborationFieldIsFinite(t *testing.T) {
	b := rates.New()
	b.Replace("bis", []rates.Observation{
		{Series: "za.policy", Area: "ZA", Type: rates.TypePolicy, Value: 7.25, Date: finNow, Source: "bis"},
		{Series: "us.policy", Area: "US", Type: rates.TypePolicy, Value: 3.625, Date: finNow, Source: "bis"},
	})
	b.Replace("sarbrates", []rates.Observation{
		{Series: "za.policy", Area: "ZA", Type: rates.TypePolicy, Value: 7.24, Date: finNow, Source: "sarbrates"},
	})
	// Two sources publishing nonsense that is nevertheless finite, so
	// rates.Materialize keeps it. Their difference is what overflows:
	// (1e307 - -1e307) * 100 is +Inf.
	b.Replace("junk-high", []rates.Observation{
		{Series: "za.policy", Area: "ZA", Type: rates.TypePolicy, Value: 1e307, Date: finNow, Source: "junk-high"},
		{Series: "us.policy", Area: "US", Type: rates.TypePolicy, Value: 1e307, Date: finNow, Source: "junk-high"},
	})
	b.Replace("junk-low", []rates.Observation{
		{Series: "za.policy", Area: "ZA", Type: rates.TypePolicy, Value: -1e307, Date: finNow, Source: "junk-low"},
		{Series: "us.policy", Area: "US", Type: rates.TypePolicy, Value: -1e307, Date: finNow, Source: "junk-low"},
	})
	snap := b.Materialize(finNow)

	checked := 0
	for _, id := range snap.IDs() {
		s, ok := snap.Lookup(id)
		if !ok {
			continue
		}
		checked++
		a := Assess(s, finNow)
		assertAssessmentEncodes(t, a)
		if a.Corroboration.Sources > 2 {
			t.Errorf("%s: corroboration counts %d sources; the junk level is not corroboration", id, a.Corroboration.Sources)
		}
	}
	// Coverage: a change that empties the book must not let this pass vacuously.
	if want := 2; checked != want {
		t.Fatalf("assessed %d series, expected %d", checked, want)
	}
}

func assertAssessmentEncodes(t *testing.T, a Assessment) {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("assessment must always be JSON-encodable, got: %v (%+v)", err, a)
	}
	// Same encoder the API streams through: if these tokens can appear at all,
	// the response is already corrupt.
	for _, bad := range []string{"NaN", "Inf"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("encoded assessment contains %q: %s", bad, b)
		}
	}
}
