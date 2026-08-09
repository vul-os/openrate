package quality

import (
	"math"
	"testing"
	"time"

	"github.com/vul-os/openrate/fx"
)

// ACCURACY.md publishes this model as a contract: consumers read `grade` and
// `confidence` off the wire and make correctness decisions with them. These
// tests pin the published numbers to the code, so the document cannot quietly
// drift away from what the engine computes.
//
// Every constant asserted here appears verbatim in ACCURACY.md. If you change
// one, change both.

// TestGradeBandsMatchDocumentedTable pins the A/B/C/D cut-offs, including that
// each band is inclusive at its lower edge.
func TestGradeBandsMatchDocumentedTable(t *testing.T) {
	for _, tc := range []struct {
		conf float64
		want string
	}{
		{1.00, "A"}, {0.90, "A"}, // A: >= 0.90
		{0.8999, "B"}, {0.78, "B"}, // B: >= 0.78
		{0.7799, "C"}, {0.60, "C"}, // C: >= 0.60
		{0.5999, "D"}, {0.00, "D"}, // D: < 0.60
	} {
		if got := grade(tc.conf); got != tc.want {
			t.Errorf("grade(%v) = %q, want %q (ACCURACY.md grade bands)", tc.conf, got, tc.want)
		}
	}
}

// TestPublishedGradeAlwaysMatchesPublishedConfidence walks every combination of
// factors the engine can produce and asserts the two published fields never
// disagree. Before this was enforced, a 2-hop exchange cross with tight
// corroboration published "confidence": 0.78 next to "grade": "C", contradicting
// the documented B >= 0.78 band.
func TestPublishedGradeAlwaysMatchesPublishedConfidence(t *testing.T) {
	freshFactors := []float64{1.0, 0.9, 0.72, 0.45}
	directFactors := []float64{1.0, 0.9, 0.75}
	classFactors := []float64{1.0, 0.96, 0.92, 0.8, 0.85, 0.7}
	corrFactors := []float64{1.0, 0.93, 0.88, 0.85, 0.72}
	caveatFactors := []float64{1.0, 0.7, 0.7 * 0.7, 0.2, 0.2 * 0.2, 0.2 * 0.7}

	checked := 0
	for _, f := range freshFactors {
		for _, d := range directFactors {
			for _, c := range classFactors {
				for _, r := range corrFactors {
					for _, v := range caveatFactors {
						checked++
						published := round2(math.Max(0, math.Min(1, f*d*c*r*v)))
						// The grade attached to a published confidence must be the
						// grade that confidence implies.
						if g := grade(published); g != bandOf(published) {
							t.Errorf("published confidence %.2f carries grade %q but the documented bands say %q",
								published, g, bandOf(published))
						}
					}
				}
			}
		}
	}
	if want := len(freshFactors) * len(directFactors) * len(classFactors) * len(corrFactors) * len(caveatFactors); checked != want {
		t.Fatalf("checked %d combinations, expected %d", checked, want)
	}
	t.Logf("verified %d factor combinations", checked)
}

// bandOf is an independent restatement of ACCURACY.md's table, deliberately
// written out rather than calling grade(), so the test does not simply agree
// with the implementation by construction.
func bandOf(conf float64) string {
	switch {
	case conf >= 0.90:
		return "A"
	case conf >= 0.78:
		return "B"
	case conf >= 0.60:
		return "C"
	}
	return "D"
}

// TestAssessPublishesSelfConsistentFields is the end-to-end version: drive the
// exact provenance that used to produce the contradiction and check the output.
func TestAssessPublishesSelfConsistentFields(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	// 20h old (current, x0.9), 2 hops (cross, x0.9), coinbase (exchange, x0.96),
	// two quotes 20 bps apart (tight, x1.0) => 0.7776.
	p := fx.Pair{Rate: 18.5, Hops: 2, AsOf: now.Add(-20 * time.Hour), Sources: []string{"coinbase"}}
	quotes := []fx.Quote{
		{Source: "coinbase", Rate: 18.50, Time: now},
		{Source: "sarb", Rate: 18.537, Time: now},
	}
	a := Assess("USD", "ZAR", p, quotes, now)
	if a.Confidence != 0.78 {
		t.Fatalf("setup drifted: confidence = %v, want 0.78", a.Confidence)
	}
	if a.Grade != "B" {
		t.Errorf("confidence 0.78 must carry grade B per ACCURACY.md, got %q", a.Grade)
	}
}

// TestSourceClassTableIsComplete pins the full source -> class mapping. The
// ACCURACY.md "Source classes" table must list every one of these.
func TestSourceClassTableIsComplete(t *testing.T) {
	want := map[string]string{
		// official (x1.0) — central-bank / official reference data
		"sarb": "official", "ecb": "official", "boc": "official", "frankfurter": "official",
		// exchange (x0.96) — venues and real-time market-data vendors
		"coinbase": "exchange", "luno": "exchange",
		"polygon": "exchange", "tradermade": "exchange", "twelvedata": "exchange",
		// aggregator (x0.92)
		"erapi": "aggregator", "fawazahmed0": "aggregator", "oxr": "aggregator",
		// unofficial (x0.7)
		"yahoo": "unofficial",
	}
	if len(sourceRank) != len(want) {
		t.Errorf("sourceRank has %d entries, this table pins %d — a source was added or removed without updating ACCURACY.md's source-class table",
			len(sourceRank), len(want))
	}
	for src, wantClass := range want {
		if _, ok := sourceRank[src]; !ok {
			t.Errorf("source %q is missing from sourceRank; it would silently grade as %q", src, "unknown")
			continue
		}
		if got, _ := sourceClass([]string{src}); got != wantClass {
			t.Errorf("sourceClass(%q) = %q, want %q", src, got, wantClass)
		}
	}
	for src := range sourceRank {
		if _, ok := want[src]; !ok {
			t.Errorf("sourceRank contains %q, which this pinned table (and ACCURACY.md) does not list", src)
		}
	}
}

// TestClassFactorsMatchDocumentation pins the multipliers ACCURACY.md prints,
// including the two "unknown" cases the document must account for.
func TestClassFactorsMatchDocumentation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources []string
		class   string
		factor  float64
	}{
		{"official", []string{"ecb"}, "official", 1.0},
		{"exchange", []string{"coinbase"}, "exchange", 0.96},
		{"aggregator", []string{"erapi"}, "aggregator", 0.92},
		{"unofficial", []string{"yahoo"}, "unofficial", 0.7},
		{"unrecognised name", []string{"nope"}, "unknown", 0.8},
		{"no sources at all", nil, "unknown", 0.85},
		{"weakest link wins", []string{"ecb", "yahoo"}, "unofficial", 0.7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cls, f := sourceClass(tc.sources)
			if cls != tc.class || f != tc.factor {
				t.Errorf("sourceClass(%v) = (%q, %v), want (%q, %v)", tc.sources, cls, f, tc.class, tc.factor)
			}
		})
	}
}

// TestCorroborationAgreeThresholdIs50Bps pins `agree`, which is a *different*
// threshold from the confidence bands (25/100/300 bps) and is therefore easy to
// misread. A consumer keying on `agree` needs this number documented.
func TestCorroborationAgreeThresholdIs50Bps(t *testing.T) {
	// Two quotes whose spread in bps is exactly the value under test.
	at := func(bps float64) (Corroboration, float64) {
		return corroborate([]fx.Quote{
			{Source: "a", Rate: 100},
			{Source: "b", Rate: 100 * (1 + bps/10000)},
		})
	}
	if c, _ := at(50); !c.Agree {
		t.Errorf("spread of exactly 50 bps must agree (threshold is <= 50), got agree=false spread=%v", c.SpreadBps)
	}
	if c, _ := at(51); c.Agree {
		t.Errorf("spread of 51 bps must not agree, got agree=true spread=%v", c.SpreadBps)
	}
	// agree=true does NOT imply the top confidence factor: 50 bps agrees but
	// only earns 0.93. Documenting this stops consumers reading `agree` as "A".
	if _, f := at(50); f != 0.93 {
		t.Errorf("a 50 bps spread earns factor %v, want 0.93", f)
	}
	if _, f := at(25); f != 1.0 {
		t.Errorf("a 25 bps spread earns factor %v, want 1.0", f)
	}
}

// TestCorroborationBandsMatchDocumentation pins the four spread bands.
func TestCorroborationBandsMatchDocumentation(t *testing.T) {
	for _, tc := range []struct {
		bps    float64
		factor float64
	}{
		{10, 1.0}, {25, 1.0}, // <= 25
		{26, 0.93}, {100, 0.93}, // <= 100
		{101, 0.85}, {300, 0.85}, // <= 300
		{301, 0.72}, {5000, 0.72}, // else
	} {
		_, f := corroborate([]fx.Quote{
			{Source: "a", Rate: 100},
			{Source: "b", Rate: 100 * (1 + tc.bps/10000)},
		})
		if f != tc.factor {
			t.Errorf("spread %v bps: factor = %v, want %v", tc.bps, f, tc.factor)
		}
	}
	// The two special cases ACCURACY.md calls out.
	if c, f := corroborate(nil); f != 1.0 || c.Sources != 0 || !c.Agree {
		t.Errorf("a purely derived pair must be neutral: got factor %v, %+v", f, c)
	}
	if c, f := corroborate([]fx.Quote{{Source: "a", Rate: 100}}); f != 0.88 || c.Agree {
		t.Errorf("a single uncorroborated source must be factor 0.88 and agree=false: got %v, %+v", f, c)
	}
}

// TestCaveatMultipliersMatchDocumentation pins the per-currency penalties and
// that each one attaches a human-readable caveat.
func TestCaveatMultipliersMatchDocumentation(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	// Perfect provenance, so confidence is exactly the caveat multiplier.
	perfect := func(from, to string) Assessment {
		p := fx.Pair{Rate: 1, Hops: 1, AsOf: now, Sources: []string{"ecb"}}
		q := []fx.Quote{{Source: "a", Rate: 100, Time: now}, {Source: "b", Rate: 100, Time: now}}
		return Assess(from, to, p, q, now)
	}
	for _, tc := range []struct {
		ccy  string
		conf float64
	}{
		{"USD", 1.0}, // no caveat
		{"NGN", 0.7}, // managed / parallel market
		{"EGP", 0.7}, //
		{"CNY", 0.7}, // managed onshore/offshore
		{"HRK", 0.2}, // defunct
	} {
		a := perfect("ZAR", tc.ccy)
		if a.Confidence != tc.conf {
			t.Errorf("%s with perfect provenance: confidence = %v, want %v", tc.ccy, a.Confidence, tc.conf)
		}
		wantCaveat := tc.conf != 1.0
		if gotCaveat := len(a.Caveats) > 0; gotCaveat != wantCaveat {
			t.Errorf("%s: caveats present = %v, want %v (%v)", tc.ccy, gotCaveat, wantCaveat, a.Caveats)
		}
	}
}

// TestCaveatCurrenciesCannotGradeA is the claim ACCURACY.md's coverage section
// makes about flagged currencies. A 0.7 multiplier caps confidence at 0.70, so
// C is the ceiling no matter how good the provenance is.
func TestCaveatCurrenciesCannotGradeA(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	for _, ccy := range []string{"NGN", "EGP", "CNY"} {
		p := fx.Pair{Rate: 1, Hops: 1, AsOf: now, Sources: []string{"ecb"}}
		q := []fx.Quote{{Source: "a", Rate: 100, Time: now}, {Source: "b", Rate: 100, Time: now}}
		a := Assess("ZAR", ccy, p, q, now)
		if a.Grade == "A" || a.Grade == "B" {
			t.Errorf("%s reached grade %s (conf %v); a flagged currency is capped at 0.70 => C", ccy, a.Grade, a.Confidence)
		}
		if a.Grade != "C" {
			t.Errorf("%s best case should be exactly C, got %s (conf %v)", ccy, a.Grade, a.Confidence)
		}
	}
	// A defunct currency is capped at 0.20 => always D.
	p := fx.Pair{Rate: 1, Hops: 1, AsOf: now, Sources: []string{"ecb"}}
	q := []fx.Quote{{Source: "a", Rate: 100, Time: now}, {Source: "b", Rate: 100, Time: now}}
	if a := Assess("ZAR", "HRK", p, q, now); a.Grade != "D" {
		t.Errorf("a defunct currency must always grade D, got %s (conf %v)", a.Grade, a.Confidence)
	}
}

// TestDocumentedProvenanceTable pins, row for row, the "What it takes to reach
// each grade" table in ACCURACY.md. If a factor changes, this fails and the
// table must be rewritten with it.
func TestDocumentedProvenanceTable(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	tight := []fx.Quote{ // 2 quotes ~16 bps apart (<= 25)
		{Source: "a", Rate: 18.50, Time: now},
		{Source: "b", Rate: 18.53, Time: now},
	}
	single := []fx.Quote{{Source: "a", Rate: 18.50, Time: now}}

	for _, tc := range []struct {
		row    string
		hops   int
		age    time.Duration
		source string
		quotes []fx.Quote
		conf   float64
		grade  string
	}{
		{"direct, realtime, exchange, >=2 quotes within 25 bps", 1, time.Minute, "coinbase", tight, 0.96, "A"},
		{"direct, daily official (current), >=2 quotes within 25 bps", 1, 20 * time.Hour, "sarb", tight, 0.90, "A"},
		{"direct, realtime, exchange, single source", 1, time.Minute, "coinbase", single, 0.84, "B"},
		{"direct, daily official, single source", 1, 20 * time.Hour, "sarb", single, 0.79, "B"},
		{"2-hop cross, daily, exchange, >=2 tight quotes", 2, 20 * time.Hour, "coinbase", tight, 0.78, "B"},
	} {
		t.Run(tc.row, func(t *testing.T) {
			p := fx.Pair{Rate: 18.5, Hops: tc.hops, AsOf: now.Add(-tc.age), Sources: []string{tc.source}}
			a := Assess("USD", "ZAR", p, tc.quotes, now)
			if a.Confidence != tc.conf || a.Grade != tc.grade {
				t.Errorf("ACCURACY.md row %q says %.2f/%s, engine says %.2f/%s",
					tc.row, tc.conf, tc.grade, a.Confidence, a.Grade)
			}
		})
	}
}

// TestGradeARequiresCorroboration documents what it actually takes to reach A,
// which ACCURACY.md's coverage section previously implied was routine.
func TestGradeARequiresCorroboration(t *testing.T) {
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	mk := func(age time.Duration, src string, quotes []fx.Quote) Assessment {
		p := fx.Pair{Rate: 18.5, Hops: 1, AsOf: now.Add(-age), Sources: []string{src}}
		return Assess("USD", "ZAR", p, quotes, now)
	}
	two := []fx.Quote{{Source: "a", Rate: 100, Time: now}, {Source: "b", Rate: 100.2, Time: now}}
	one := []fx.Quote{{Source: "a", Rate: 100, Time: now}}

	// Direct + realtime + exchange, but a single source: 1.0*1.0*0.96*0.88 = 0.84.
	if a := mk(time.Minute, "coinbase", one); a.Grade == "A" {
		t.Errorf("a single uncorroborated source must not reach A, got %s (%v)", a.Grade, a.Confidence)
	}
	// Add a second agreeing source and it reaches A.
	if a := mk(time.Minute, "coinbase", two); a.Grade != "A" {
		t.Errorf("direct+realtime+exchange with 2 tight quotes should be A, got %s (%v)", a.Grade, a.Confidence)
	}
	// A daily official quote reaches A only on the nose (0.9 exactly).
	if a := mk(20*time.Hour, "sarb", two); a.Grade != "A" || a.Confidence != 0.9 {
		t.Errorf("daily official + 2 tight quotes should be exactly 0.90/A, got %s (%v)", a.Grade, a.Confidence)
	}
	// One hop more and it is no longer A.
	p := fx.Pair{Rate: 18.5, Hops: 2, AsOf: now.Add(-20 * time.Hour), Sources: []string{"sarb"}}
	if a := Assess("USD", "ZAR", p, two, now); a.Grade == "A" {
		t.Errorf("a triangulated daily pair must not reach A, got %s (%v)", a.Grade, a.Confidence)
	}
}
