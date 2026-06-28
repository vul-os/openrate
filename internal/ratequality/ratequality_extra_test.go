package ratequality

import (
	"testing"
	"time"

	"github.com/vul-os/openrate/internal/rates"
)

var rqNow = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

// ─── Freshness boundaries ────────────────────────────────────────────────────

func TestFreshnessBoundaries(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		age   time.Duration
		label string
	}{
		{3*day - time.Second, "current"}, // just under 3d
		{3 * day, "recent"},              // at 3d exactly → next tier
		{16*day - time.Second, "recent"}, // just under 16d
		{16 * day, "aging"},              // at 16d
		{50*day - time.Second, "aging"},  // just under 50d
		{50 * day, "stale"},              // at 50d
		{150*day - time.Second, "stale"}, // just under 150d
		{150 * day, "old"},               // at 150d
	}
	for _, tc := range cases {
		label, _ := freshness(tc.age)
		if label != tc.label {
			t.Errorf("age=%v: freshness = %q, want %q", tc.age, label, tc.label)
		}
	}
}

func TestFreshnessFactors(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		age    time.Duration
		factor float64
	}{
		{time.Hour, 1.0}, // current
		{5 * day, 0.97},  // recent
		{20 * day, 0.82}, // aging
		{100 * day, 0.6}, // stale
		{200 * day, 0.4}, // old
	}
	for _, tc := range cases {
		_, f := freshness(tc.age)
		if f != tc.factor {
			t.Errorf("age=%v: factor = %v, want %v", tc.age, f, tc.factor)
		}
	}
}

// ─── Corroboration (interest-rate specific: absolute bps) ────────────────────

func TestRatequalityCorroborationMeanAndSpread(t *testing.T) {
	// Two sources: 7.00 and 7.10 → spread = (7.10-7.00)*100 = 10 bps.
	quotes := []rates.Quote{
		{Source: "bis", Value: 7.00, Date: rqNow},
		{Source: "sarbrates", Value: 7.10, Date: rqNow},
	}
	corr, _ := corroborate(quotes)
	if corr.Sources != 2 {
		t.Errorf("sources = %d, want 2", corr.Sources)
	}
	wantSpread := 10.0
	if corr.SpreadBps != wantSpread {
		t.Errorf("spread = %v bps, want %v", corr.SpreadBps, wantSpread)
	}
	wantMean := 7.05
	if corr.Mean != wantMean {
		t.Errorf("mean = %v, want %v", corr.Mean, wantMean)
	}
}

func TestRatequalityCorroborationAgreeWithinTolerance(t *testing.T) {
	// Spread = (7.04-7.03)*100 = 1 bps → agree (≤ 5 bps).
	quotes := []rates.Quote{
		{Source: "bis", Value: 7.04},
		{Source: "sarbrates", Value: 7.03},
	}
	corr, _ := corroborate(quotes)
	if !corr.Agree {
		t.Errorf("1 bps spread: agree must be true (≤5 bps tolerance), spread=%v", corr.SpreadBps)
	}
}

func TestRatequalityCorroborationDisagreesAboveTolerance(t *testing.T) {
	// Spread = (7.25-7.00)*100 = 25 bps > 5 → agree=false.
	quotes := []rates.Quote{
		{Source: "bis", Value: 7.25},
		{Source: "sarbrates", Value: 7.00},
	}
	corr, _ := corroborate(quotes)
	if corr.Agree {
		t.Errorf("25 bps spread: agree must be false, got spread=%v", corr.SpreadBps)
	}
}

func TestRatequalitySingleSourceFactor(t *testing.T) {
	quotes := []rates.Quote{{Source: "bis", Value: 7.0}}
	_, f := corroborate(quotes)
	if f != 0.9 {
		t.Errorf("single-source factor = %v, want 0.9", f)
	}
}

func TestRatequalityZeroQuotesFactor(t *testing.T) {
	_, f := corroborate(nil)
	if f != 1.0 {
		t.Errorf("zero-quotes factor = %v, want 1.0", f)
	}
}

// ─── Area caveats ─────────────────────────────────────────────────────────────

func TestAreaCaveatUS(t *testing.T) {
	s := rates.Series{
		Series: "us.policy", Area: "US", Type: rates.TypePolicy,
		Value: 5.25, Date: rqNow, Source: "fred",
		Latest: []rates.Quote{{Source: "fred", Value: 5.25, Date: rqNow}},
	}
	a := Assess(s, rqNow)
	found := false
	for _, c := range a.Caveats {
		if len(c) > 0 {
			found = true
			break
		}
	}
	if !found {
		t.Error("US policy series must have the target-range caveat")
	}
}

func TestAreaCaveatCN(t *testing.T) {
	s := rates.Series{
		Series: "cn.policy", Area: "CN", Type: rates.TypePolicy,
		Value: 3.45, Date: rqNow, Source: "bis",
		Latest: []rates.Quote{{Source: "bis", Value: 3.45, Date: rqNow}},
	}
	a := Assess(s, rqNow)
	if len(a.Caveats) == 0 {
		t.Error("CN series must have managed-regime caveat")
	}
}

func TestAreaCaveatAR(t *testing.T) {
	s := rates.Series{
		Series: "ar.policy", Area: "AR", Type: rates.TypePolicy,
		Value: 118.0, Date: rqNow, Source: "bis",
		Latest: []rates.Quote{{Source: "bis", Value: 118.0, Date: rqNow}},
	}
	a := Assess(s, rqNow)
	if len(a.Caveats) == 0 {
		t.Error("AR series must have high-volatility caveat")
	}
}

// ─── Index type caveat ────────────────────────────────────────────────────────

func TestIndexTypeCaveat(t *testing.T) {
	s := rates.Series{
		Series: "za.index", Area: "ZA", Type: "index",
		Value: 123.4, Date: rqNow, Source: "bis",
		Latest: []rates.Quote{{Source: "bis", Value: 123.4, Date: rqNow}},
	}
	a := Assess(s, rqNow)
	found := false
	for _, c := range a.Caveats {
		if c == "this series is an index level, not an annualised rate" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("index-type series must carry caveat, got caveats=%v", a.Caveats)
	}
}

// ─── Source class (interest-rate authority) ───────────────────────────────────

func TestRatequalitySourceClassOfficial(t *testing.T) {
	cls, _ := sourceClass("sarbrates")
	if cls != "official_issuer" {
		t.Errorf("sarbrates class = %q, want official_issuer", cls)
	}
}

func TestRatequalitySourceClassAggregator(t *testing.T) {
	cls, _ := sourceClass("bis")
	if cls != "official_aggregator" {
		t.Errorf("bis class = %q, want official_aggregator", cls)
	}
}

func TestRatequalitySourceClassUnknown(t *testing.T) {
	cls, _ := sourceClass("unknown-provider")
	if cls != "unknown" {
		t.Errorf("unknown source class = %q, want unknown", cls)
	}
}

// ─── Grade thresholds ─────────────────────────────────────────────────────────

func TestRatequalityGradeThresholds(t *testing.T) {
	cases := []struct {
		conf float64
		want string
	}{
		{0.95, "A"},
		{0.90, "A"},
		{0.89, "B"},
		{0.78, "B"},
		{0.77, "C"},
		{0.60, "C"},
		{0.59, "D"},
	}
	for _, tc := range cases {
		if got := grade(tc.conf); got != tc.want {
			t.Errorf("grade(%.2f) = %q, want %q", tc.conf, got, tc.want)
		}
	}
}

// ─── Confidence bounds ────────────────────────────────────────────────────────

func TestRatequalityConfidenceBounds(t *testing.T) {
	// Multiple penalty factors compounding: stale+single+area caveat+unknown class.
	s := rates.Series{
		Series: "ar.policy", Area: "AR", Type: rates.TypePolicy,
		Value: 200.0, Date: rqNow.AddDate(0, 0, -200), Source: "unknownsrc",
		Latest: []rates.Quote{{Source: "unknownsrc", Value: 200.0, Date: rqNow.AddDate(0, 0, -200)}},
	}
	a := Assess(s, rqNow)
	if a.Confidence < 0 || a.Confidence > 1 {
		t.Errorf("confidence = %v is outside [0, 1]", a.Confidence)
	}
}

// ─── round2 ──────────────────────────────────────────────────────────────────

func TestRound2(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{0.123456, 0.12},
		{0.125, 0.13}, // rounds up (math.Round is half-away-from-zero)
		{0.999, 1.0},
		{7.055 * 100 / 100, 7.06},
	}
	for _, tc := range cases {
		if got := round2(tc.in); got != tc.want {
			t.Errorf("round2(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ─── Explain ─────────────────────────────────────────────────────────────────

func TestRatequalityExplainNonEmpty(t *testing.T) {
	s := rates.Series{
		Series: "us.policy", Area: "US", Type: rates.TypePolicy,
		Value: 5.25, Date: rqNow, Source: "fred",
		Latest: []rates.Quote{{Source: "fred", Value: 5.25, Date: rqNow}},
	}
	a := Assess(s, rqNow)
	if a.Explain() == "" {
		t.Error("Explain must return a non-empty string")
	}
}
