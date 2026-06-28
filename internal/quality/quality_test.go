package quality

import (
	"testing"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

var qNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// mkPair builds a graph.Pair with the given hops, sources, and AsOf timestamp.
func mkPair(hops int, sources []string, asOf time.Time) graph.Pair {
	return graph.Pair{Hops: hops, Sources: sources, AsOf: asOf}
}

// ─── Freshness ───────────────────────────────────────────────────────────────

func TestFreshnessTiers(t *testing.T) {
	cases := []struct {
		age   time.Duration
		label string
	}{
		{2 * time.Minute, "realtime"},            // < 5m
		{5*time.Minute + time.Second, "current"}, // [5m, 26h)
		{26*time.Hour + time.Second, "daily"},    // [26h, 4d)
		{5 * 24 * time.Hour, "stale"},            // ≥ 4d
	}
	for _, tc := range cases {
		label, _ := freshness(tc.age)
		if label != tc.label {
			t.Errorf("age=%v: freshness = %q, want %q", tc.age, label, tc.label)
		}
	}
}

func TestFreshnessBoundaryAtFiveMinutes(t *testing.T) {
	// Exactly 5 minutes: not < 5m, so falls to "current".
	label, _ := freshness(5 * time.Minute)
	if label != "current" {
		t.Errorf("at exactly 5min: want current, got %q", label)
	}
	// One nanosecond under the boundary: still realtime.
	label2, _ := freshness(5*time.Minute - time.Nanosecond)
	if label2 != "realtime" {
		t.Errorf("at 5min-1ns: want realtime, got %q", label2)
	}
}

func TestFreshnessBoundaryAt26Hours(t *testing.T) {
	label, _ := freshness(26 * time.Hour)
	if label != "daily" {
		t.Errorf("at exactly 26h: want daily, got %q", label)
	}
	label2, _ := freshness(26*time.Hour - time.Nanosecond)
	if label2 != "current" {
		t.Errorf("at 26h-1ns: want current, got %q", label2)
	}
}

// ─── Directness ──────────────────────────────────────────────────────────────

func TestDirectnessTiers(t *testing.T) {
	cases := []struct {
		hops  int
		label string
	}{
		{0, "direct"},
		{1, "direct"},
		{2, "cross"},
		{3, "multi_cross"},
		{5, "multi_cross"},
	}
	for _, tc := range cases {
		label, _ := directness(tc.hops)
		if label != tc.label {
			t.Errorf("hops=%d: directness = %q, want %q", tc.hops, label, tc.label)
		}
	}
}

func TestDirectnessFactor(t *testing.T) {
	_, f1 := directness(1)
	if f1 != 1.0 {
		t.Errorf("direct (1-hop) factor = %v, want 1.0", f1)
	}
	_, f2 := directness(2)
	if f2 != 0.9 {
		t.Errorf("cross (2-hop) factor = %v, want 0.9", f2)
	}
	_, f3 := directness(3)
	if f3 != 0.75 {
		t.Errorf("multi_cross (3-hop) factor = %v, want 0.75", f3)
	}
}

// ─── Source class ────────────────────────────────────────────────────────────

func TestSourceClassOfficialFactor(t *testing.T) {
	name, f := sourceClass([]string{"sarb"})
	if name != "official" {
		t.Errorf("sarb class = %q, want official", name)
	}
	if f != 1.0 {
		t.Errorf("official factor = %v, want 1.0", f)
	}
}

func TestSourceClassExchange(t *testing.T) {
	name, _ := sourceClass([]string{"coinbase"})
	if name != "exchange" {
		t.Errorf("coinbase class = %q, want exchange", name)
	}
}

func TestSourceClassAggregator(t *testing.T) {
	name, _ := sourceClass([]string{"erapi"})
	if name != "aggregator" {
		t.Errorf("erapi class = %q, want aggregator", name)
	}
}

func TestSourceClassWeakestLinkWins(t *testing.T) {
	// Path through official + unofficial: rated as unofficial (weakest link).
	name, f := sourceClass([]string{"sarb", "yahoo"})
	if name != "unofficial" {
		t.Errorf("weakest link: class = %q, want unofficial", name)
	}
	if f != 0.7 {
		t.Errorf("unofficial factor = %v, want 0.7", f)
	}
}

func TestSourceClassUnknown(t *testing.T) {
	name, _ := sourceClass([]string{"some-unknown-source"})
	if name != "unknown" {
		t.Errorf("unknown source: class = %q, want unknown", name)
	}
}

func TestSourceClassEmptySources(t *testing.T) {
	name, _ := sourceClass(nil)
	if name != "unknown" {
		t.Errorf("empty sources: class = %q, want unknown", name)
	}
}

// ─── Corroboration ───────────────────────────────────────────────────────────

func TestCorroborationZeroQuotes(t *testing.T) {
	corr, f := corroborate(nil)
	if corr.Sources != 0 {
		t.Errorf("sources = %d, want 0", corr.Sources)
	}
	if !corr.Agree {
		t.Error("0 quotes: agree must be true (no sources, no disagreement)")
	}
	if f != 1.0 {
		t.Errorf("factor = %v, want 1.0 for zero quotes", f)
	}
}

func TestCorroborationOneSource(t *testing.T) {
	quotes := []graph.Quote{{Source: "sarb", Rate: 18.5}}
	corr, f := corroborate(quotes)
	if corr.Sources != 1 {
		t.Errorf("sources = %d, want 1", corr.Sources)
	}
	if corr.Agree {
		t.Error("single source: agree must be false (uncorroborated)")
	}
	if f != 0.88 {
		t.Errorf("single-source factor = %v, want 0.88", f)
	}
}

func TestCorroborationTightSpread(t *testing.T) {
	// Spread = (18.50 - 18.48)/18.48*10000 ≈ 10.8 bps (≤ 25 → agree=true, factor=1.0).
	quotes := []graph.Quote{
		{Source: "sarb", Rate: 18.50},
		{Source: "ecb", Rate: 18.48},
	}
	corr, f := corroborate(quotes)
	if !corr.Agree {
		t.Errorf("tight spread (%v bps): agree must be true", corr.SpreadBps)
	}
	if f != 1.0 {
		t.Errorf("tight spread factor = %v, want 1.0 (≤25 bps)", f)
	}
}

func TestCorroborationWideSpreadsDisagrees(t *testing.T) {
	// Spread = (18.50 - 17.50)/17.50*10000 ≈ 571 bps (> 50 → agree=false).
	quotes := []graph.Quote{
		{Source: "sarb", Rate: 18.50},
		{Source: "yahoo", Rate: 17.50},
	}
	corr, f := corroborate(quotes)
	if corr.Agree {
		t.Errorf("wide spread (%v bps): agree must be false", corr.SpreadBps)
	}
	if f == 1.0 {
		t.Error("wide spread factor must be < 1.0")
	}
}

func TestCorroborationDuplicateSourceDeduped(t *testing.T) {
	// Two quotes from the same source → effectively single-source.
	quotes := []graph.Quote{
		{Source: "sarb", Rate: 18.50},
		{Source: "sarb", Rate: 18.51}, // overwritten by the map
	}
	corr, _ := corroborate(quotes)
	if corr.Sources != 1 {
		t.Errorf("duplicate source: effective sources = %d, want 1", corr.Sources)
	}
}

func TestCorroborationZeroRateIgnored(t *testing.T) {
	// Zero-rate quotes must be ignored (not a valid market rate).
	quotes := []graph.Quote{
		{Source: "sarb", Rate: 0},
		{Source: "ecb", Rate: 18.5},
	}
	corr, _ := corroborate(quotes)
	if corr.Sources != 1 {
		t.Errorf("zero-rate quote: effective sources = %d, want 1", corr.Sources)
	}
}

// ─── Caveats ─────────────────────────────────────────────────────────────────

func TestManagedRateCaveatNGN(t *testing.T) {
	p := mkPair(1, []string{"sarb"}, qNow.Add(-time.Minute))
	a := Assess("USD", "NGN", p, nil, qNow)
	if len(a.Caveats) == 0 {
		t.Error("NGN must trigger a managed-rate caveat")
	}
}

func TestManagedRateCaveatCNY(t *testing.T) {
	p := mkPair(1, []string{"sarb"}, qNow.Add(-time.Minute))
	a := Assess("USD", "CNY", p, nil, qNow)
	if len(a.Caveats) == 0 {
		t.Error("CNY must trigger a managed-rate caveat")
	}
}

func TestDefunctCurrencyHRK(t *testing.T) {
	p := mkPair(1, []string{"sarb"}, qNow.Add(-time.Minute))
	a := Assess("EUR", "HRK", p, nil, qNow)
	if len(a.Caveats) == 0 {
		t.Error("HRK must trigger a defunct-currency caveat")
	}
	// Defunct currencies get a 0.2× multiplier — grade must be D.
	if a.Grade != "D" {
		t.Errorf("defunct currency: grade = %q, want D", a.Grade)
	}
}

// ─── Grade thresholds ─────────────────────────────────────────────────────────

func TestGradeThresholds(t *testing.T) {
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
		{0.00, "D"},
	}
	for _, tc := range cases {
		if got := grade(tc.conf); got != tc.want {
			t.Errorf("grade(%.2f) = %q, want %q", tc.conf, got, tc.want)
		}
	}
}

// ─── Confidence bounds ───────────────────────────────────────────────────────

func TestConfidenceClampedTo01(t *testing.T) {
	// Two defunct currencies + stale + unofficial → would mathematically go
	// negative without clamping.
	p := mkPair(3, []string{"yahoo"}, qNow.Add(-8*24*time.Hour))
	a := Assess("HRK", "HRK", p, nil, qNow)
	if a.Confidence < 0 || a.Confidence > 1 {
		t.Errorf("confidence = %v outside [0, 1]", a.Confidence)
	}
}

// ─── End-to-end Assess cases ─────────────────────────────────────────────────

func TestAssessHighConfidenceCase(t *testing.T) {
	// Fresh (<5 min), direct (1 hop), official, corroborated → must grade A.
	quotes := []graph.Quote{
		{Source: "sarb", Rate: 18.50},
		{Source: "ecb", Rate: 18.51},
	}
	p := graph.Pair{
		Hops:    1,
		Sources: []string{"sarb", "ecb"},
		AsOf:    qNow.Add(-2 * time.Minute),
	}
	a := Assess("USD", "ZAR", p, quotes, qNow)
	if a.Grade != "A" {
		t.Errorf("fresh+direct+official+corroborated: grade = %q, want A (conf=%.2f)", a.Grade, a.Confidence)
	}
}

func TestAssessLowConfidenceCase(t *testing.T) {
	// Stale (8 days), multi-cross (3 hops), unofficial, single source → grade D.
	p := graph.Pair{
		Hops:    3,
		Sources: []string{"yahoo"},
		AsOf:    qNow.Add(-8 * 24 * time.Hour),
	}
	quotes := []graph.Quote{{Source: "yahoo", Rate: 18.0}}
	a := Assess("USD", "ZAR", p, quotes, qNow)
	if a.Grade != "D" {
		t.Errorf("stale+multi_cross+unofficial: grade = %q, want D (conf=%.2f)", a.Grade, a.Confidence)
	}
}

func TestAssessExplainNotEmpty(t *testing.T) {
	p := mkPair(1, []string{"sarb"}, qNow.Add(-time.Minute))
	a := Assess("USD", "ZAR", p, nil, qNow)
	if a.Explain() == "" {
		t.Error("Explain must return a non-empty string")
	}
}
