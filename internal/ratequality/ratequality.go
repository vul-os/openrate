// Package ratequality turns an interest-rate series' provenance into an
// explainable confidence grade. It is the interest-rate counterpart to the FX
// quality package, but the relevant factors differ: there is no triangulation
// (so no "directness"/hops), freshness is judged against publication cadence
// rather than market tick-rate, and corroboration compares rate *levels* in
// absolute basis points rather than a relative FX spread.
//
// Factors (multiplicative):
//   - source authority — issuing central bank > official aggregator > commercial
//   - freshness        — how recently the headline value was published
//   - corroboration    — how many independent sources agree, and by how many bps
//   - caveats          — definitional notes (target ranges, managed regimes, …)
package ratequality

import (
	"math"
	"time"

	"github.com/vul-os/openrate/internal/rates"
)

// Assessment is the per-series confidence report attached to API responses.
type Assessment struct {
	Grade         string        `json:"grade"`        // A | B | C | D
	Confidence    float64       `json:"confidence"`   // 0..1
	Freshness     string        `json:"freshness"`    // current | recent | aging | stale | old
	SourceClass   string        `json:"source_class"` // official_issuer | official_aggregator | commercial | unofficial | unknown
	Corroboration Corroboration `json:"corroboration"`
	Caveats       []string      `json:"caveats,omitempty"`
}

// Corroboration captures cross-source agreement on the headline level.
type Corroboration struct {
	Sources   int     `json:"sources"`    // number of independent sources for this series
	SpreadBps float64 `json:"spread_bps"` // (max-min) across sources, in basis points
	Agree     bool    `json:"agree"`      // spread within tolerance
	Min       float64 `json:"min,omitempty"`
	Max       float64 `json:"max,omitempty"`
	Mean      float64 `json:"mean,omitempty"`
}

var classNames = map[int]string{
	4: "official_issuer",
	3: "official_aggregator",
	2: "commercial",
	1: "unofficial",
	0: "unknown",
}

var classFactor = map[int]float64{4: 1.0, 3: 0.97, 2: 0.9, 1: 0.75, 0: 0.85}

// areaCaveat flags series whose headline value carries a standing definitional
// or regime caveat. Keyed by ISO area code.
var areaCaveat = map[string]string{
	"US": "US policy rate is a target range; the published value is the midpoint",
	"CN": "Chinese rates are administratively managed; onshore and offshore conditions differ",
	"AR": "Argentine rates are highly volatile under high inflation; values move fast",
	"TR": "Turkish rates are highly volatile under high inflation; values move fast",
}

// Assess builds the confidence report for a materialized series.
func Assess(s rates.Series, now time.Time) Assessment {
	conf := 1.0

	cls, cf := sourceClass(s.Source)
	conf *= cf

	fresh, ff := freshness(now.Sub(s.Date))
	conf *= ff

	corr, rf := corroborate(s.Latest)
	conf *= rf

	var caveats []string
	if msg, ok := areaCaveat[s.Area]; ok {
		caveats = append(caveats, msg)
		conf *= 0.92
	}
	if s.Type == "index" {
		caveats = append(caveats, "this series is an index level, not an annualised rate")
	}
	if corr.Sources <= 1 {
		caveats = append(caveats, "single source — not independently corroborated")
	}

	// Grade the confidence that is actually published, so the two fields can
	// never contradict each other at a band edge. Same reasoning as the FX
	// quality package; see docs/interest-rates.md for the bands.
	conf = math.Max(0, math.Min(1, conf))
	published := round2(conf)
	return Assessment{
		Grade:         grade(published),
		Confidence:    published,
		Freshness:     fresh,
		SourceClass:   cls,
		Corroboration: corr,
		Caveats:       caveats,
	}
}

func sourceClass(source string) (string, float64) {
	r := rates.Rank(source)
	return classNames[r], classFactor[r]
}

// freshness grades by publication age. Policy rates can sit unchanged for months,
// but a healthy feed still republishes the carried-forward value within a day or
// two; a large age means the feed itself is lagging, which is what we penalise.
func freshness(age time.Duration) (string, float64) {
	day := 24 * time.Hour
	switch {
	case age < 3*day:
		return "current", 1.0
	case age < 16*day: // absorbs weekly aggregator cadence + weekends/holidays
		return "recent", 0.97
	case age < 50*day:
		return "aging", 0.82
	case age < 150*day:
		return "stale", 0.6
	default:
		return "old", 0.4
	}
}

// corroborate compares the latest value reported by each independent source for
// the series. Dispersion is measured in absolute basis points (1 percentage
// point = 100 bps), since interest rates are levels, not ratios.
func corroborate(quotes []rates.Quote) (Corroboration, float64) {
	// Only levels we can do arithmetic on (see usableLevel). rates.Materialize
	// already refuses a NaN/Inf observation, but Assess takes a Series directly
	// and nothing stops a caller — or a future second producer of Series — from
	// handing us one, and an unfiltered NaN makes min/max NaN and the whole
	// Assessment unencodable.
	seen := map[string]float64{}
	for _, q := range quotes {
		if usableLevel(q.Value) {
			seen[q.Source] = q.Value
		}
	}
	n := len(seen)
	if n == 0 {
		return Corroboration{Sources: 0, Agree: true}, 1.0
	}
	if n == 1 {
		return Corroboration{Sources: 1, SpreadBps: 0, Agree: false}, 0.9
	}
	min, max, sum := math.Inf(1), math.Inf(-1), 0.0
	for _, v := range seen {
		min = math.Min(min, v)
		max = math.Max(max, v)
		sum += v
	}
	spreadBps := boundedBps((max - min) * 100) // percentage points -> basis points
	mean := sum / float64(n)
	factor, agree := spreadBand(spreadBps)
	return Corroboration{
		Sources: n, SpreadBps: round2(spreadBps), Agree: agree,
		Min: min, Max: max, Mean: round2(mean),
	}, factor
}

// spreadBand maps an absolute dispersion in bps to the confidence factor and the
// agreement flag. It is a separate function so the guarantee boundedBps relies
// on is directly testable: the clamped value must land in the worst factor
// bucket and must not agree.
func spreadBand(spreadBps float64) (factor float64, agree bool) {
	agree = spreadBps <= 5
	switch {
	case spreadBps <= 2:
		factor = 1.0
	case spreadBps <= 10:
		factor = 0.95
	case spreadBps <= 25:
		factor = 0.88
	default:
		factor = 0.78
	}
	return factor, agree
}

func grade(conf float64) string {
	switch {
	case conf >= 0.9:
		return "A"
	case conf >= 0.78:
		return "B"
	case conf >= 0.6:
		return "C"
	default:
		return "D"
	}
}

// A published level must be bounded in magnitude, not merely finite — the same
// argument as fx's rate band (see usable in fx/graph.go), with the interest-rate
// shape of the arithmetic: the divergent term here is (max-min)*100, which is
// +Inf for any pair of levels more than ~1.8e306 apart, and round2's f*100
// overflows for any |f| >= ~1.8e306. Both produce a number json.Marshal refuses,
// which under serve's encode-then-write is a 500 on a series whose only problem
// is one absurd value from one source.
//
// Unlike an FX rate, a level is a percentage and is routinely negative (the ECB
// deposit rate sat at -0.5 for years), so the band is symmetric about zero and
// the lower end must stay open — there is no reciprocal here to protect. Real
// policy and reference rates live in about [-5, 1000]; the loose end is
// Type == "index", where a level is an index reading rather than a percentage
// and can carry many digits. 1e12 leaves nine orders of magnitude of headroom
// over any index level a feed publishes, and anything past it is a parse or
// scaling error, not a rate.
//
// Worst case with |v| <= 1e12 over n sources: (max-min)*100 <= 2e14,
// sum <= n*1e12 (+Inf needs n > 1.8e296: unreachable), mean <= 1e12, and
// round2's largest intermediate is 2e16 — 292 orders of magnitude below
// overflow, so every field of Corroboration is finite by construction.
const maxLevel = 1e12

// usableLevel reports whether a published level is inside that band. NaN fails
// (every comparison with NaN is false) and so does ±Inf, so this subsumes the
// finiteness test.
func usableLevel(v float64) bool {
	return math.Abs(v) <= maxLevel
}

// boundedBps is the second line of defence on the dispersion figure. usableLevel
// caps it at 2e14, so this cannot fire today; it is here so that widening the
// band later cannot turn a dispersion into a token json.Marshal refuses.
//
// A dispersion we could not compute is reported as the largest representable
// one, never as a small one: math.MaxFloat64 encodes as an ordinary JSON number,
// lands in the widest band (factor 0.78) and is far above the 5 bps agreement
// threshold, so a clamped value can never read as agreement or as better quality
// than was actually measured.
func boundedBps(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return math.MaxFloat64
	}
	return x
}

// round2 rounds to two decimals for publication. f*100 overflows to ±Inf for any
// |f| >= ~1.8e306, so the naive expression turns a finite number the encoder
// could emit into one it cannot. Returning such an input unchanged is the
// correct answer rather than a fallback — above 2^53/100 (~9.0e13) a float64 has
// no fractional part left, so it already is its own two-decimal rounding. The
// common path (confidence in 0..1, mean and spread of real levels) takes the
// same expression as before, bit for bit.
func round2(f float64) float64 {
	scaled := f * 100
	if math.IsInf(scaled, 0) {
		return f
	}
	return math.Round(scaled) / 100
}
