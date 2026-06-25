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
	"fmt"
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

	conf = math.Max(0, math.Min(1, conf))
	return Assessment{
		Grade:         grade(conf),
		Confidence:    round2(conf),
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
	seen := map[string]float64{}
	for _, q := range quotes {
		seen[q.Source] = q.Value
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
	spreadBps := (max - min) * 100 // percentage points -> basis points
	mean := sum / float64(n)
	agree := spreadBps <= 5
	var factor float64
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
	return Corroboration{
		Sources: n, SpreadBps: round2(spreadBps), Agree: agree,
		Min: min, Max: max, Mean: round2(mean),
	}, factor
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

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// Explain returns a one-line human summary (used in docs/tooltips).
func (a Assessment) Explain() string {
	return fmt.Sprintf("grade %s (%.0f%%): %s, %s, %d corroborating",
		a.Grade, a.Confidence*100, a.Freshness, a.SourceClass, a.Corroboration.Sources)
}
