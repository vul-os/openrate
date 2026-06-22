// Package quality turns a rate's provenance into an explainable accuracy grade.
// An FX number is only as good as where it came from, how fresh it is, how many
// hops it was triangulated through, and whether independent sources agree. We
// surface all of that with every price so consumers can decide if it's good
// enough for their use.
package quality

import (
	"fmt"
	"math"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// Assessment is the per-rate accuracy report attached to API responses.
type Assessment struct {
	Grade         string        `json:"grade"`        // A | B | C | D
	Confidence    float64       `json:"confidence"`   // 0..1
	Freshness     string        `json:"freshness"`    // realtime | current | daily | stale
	Directness    string        `json:"directness"`   // direct | cross | multi_cross
	SourceClass   string        `json:"source_class"` // official | exchange | aggregator | unofficial
	Corroboration Corroboration `json:"corroboration"`
	Caveats       []string      `json:"caveats,omitempty"`
}

// Corroboration captures cross-source agreement for the exact pair.
type Corroboration struct {
	Sources   int     `json:"sources"`    // number of independent direct quotes
	SpreadBps float64 `json:"spread_bps"` // (max-min)/min across those quotes, in basis points
	Agree     bool    `json:"agree"`      // spread within tolerance
}

// sourceRank ranks source authority (higher = more authoritative). The weakest
// link on a path determines the reported source class.
var sourceRank = map[string]int{
	"sarb": 4, "ecb": 4, "boc": 4, "frankfurter": 4, // central-bank / official data
	"coinbase": 3, "luno": 3, // real exchange venues
	"erapi": 2, "fawazahmed0": 2, // aggregators
	"yahoo": 1, // unofficial
}

var classNames = map[int]string{4: "official", 3: "exchange", 2: "aggregator", 1: "unofficial", 0: "unknown"}

// managedRate currencies have a meaningful gap between the official/reference
// rate we can see and the rate people actually transact at (parallel markets,
// managed pegs). The number is valid but may not be the street rate.
var managedRate = map[string]string{
	"NGN": "Nigerian naira: official and parallel-market rates can differ materially",
	"EGP": "Egyptian pound: official and parallel-market rates can differ materially",
	"CNY": "Chinese yuan is managed; onshore (CNY) and offshore (CNH) rates differ",
}

// defunct currencies no longer trade; any value is legacy.
var defunct = map[string]string{
	"HRK": "Croatian kuna is defunct (Croatia adopted the euro in 2023)",
}

// Assess builds the accuracy report for a pair from->to.
func Assess(from, to string, p graph.Pair, quotes []graph.Quote, now time.Time) Assessment {
	conf := 1.0

	// Freshness from the oldest edge on the path.
	age := now.Sub(p.AsOf)
	fresh, ff := freshness(age)
	conf *= ff

	// Directness from hop count (compounded spread).
	direct, df := directness(p.Hops)
	conf *= df

	// Source authority = weakest link on the path.
	cls, sf := sourceClass(p.Sources)
	conf *= sf

	// Cross-source agreement for the exact pair.
	corr, cf := corroborate(quotes)
	conf *= cf

	// Currency-specific caveats.
	var caveats []string
	for _, c := range []string{from, to} {
		if msg, ok := defunct[c]; ok {
			caveats = append(caveats, msg)
			conf *= 0.2
		} else if msg, ok := managedRate[c]; ok {
			caveats = append(caveats, msg)
			conf *= 0.7
		}
	}

	conf = math.Max(0, math.Min(1, conf))
	return Assessment{
		Grade:         grade(conf),
		Confidence:    round2(conf),
		Freshness:     fresh,
		Directness:    direct,
		SourceClass:   cls,
		Corroboration: corr,
		Caveats:       caveats,
	}
}

func freshness(age time.Duration) (string, float64) {
	switch {
	case age < 5*time.Minute:
		return "realtime", 1.0
	case age < 26*time.Hour:
		return "current", 0.9
	case age < 4*24*time.Hour: // a weekend gap still counts as the latest daily fix
		return "daily", 0.72
	default:
		return "stale", 0.45
	}
}

func directness(hops int) (string, float64) {
	switch {
	case hops <= 1:
		return "direct", 1.0
	case hops == 2:
		return "cross", 0.9
	default:
		return "multi_cross", 0.75
	}
}

func sourceClass(sources []string) (string, float64) {
	if len(sources) == 0 {
		return "unknown", 0.85
	}
	min := 5
	for _, s := range sources {
		r := sourceRank[s]
		if r < min {
			min = r
		}
	}
	if min == 5 {
		min = 0
	}
	factor := map[int]float64{4: 1.0, 3: 0.96, 2: 0.92, 1: 0.7, 0: 0.8}[min]
	return classNames[min], factor
}

func corroborate(quotes []graph.Quote) (Corroboration, float64) {
	// Distinct sources only.
	seen := map[string]float64{}
	for _, q := range quotes {
		if q.Rate > 0 {
			seen[q.Source] = q.Rate
		}
	}
	n := len(seen)
	if n == 0 {
		return Corroboration{Sources: 0, Agree: true}, 1.0 // purely derived; directness already accounts for it
	}
	if n == 1 {
		return Corroboration{Sources: 1, SpreadBps: 0, Agree: false}, 0.88 // single source, uncorroborated
	}
	min, max := math.Inf(1), math.Inf(-1)
	for _, r := range seen {
		min = math.Min(min, r)
		max = math.Max(max, r)
	}
	spread := (max - min) / min * 10000 // bps
	agree := spread <= 50
	var factor float64
	switch {
	case spread <= 25:
		factor = 1.0
	case spread <= 100:
		factor = 0.93
	case spread <= 300:
		factor = 0.85
	default:
		factor = 0.72
	}
	return Corroboration{Sources: n, SpreadBps: round2(spread), Agree: agree}, factor
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

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

// Explain returns a one-line human summary (used in docs/tooltips).
func (a Assessment) Explain() string {
	return fmt.Sprintf("grade %s (%.0f%%): %s, %s, %s source, %d corroborating",
		a.Grade, a.Confidence*100, a.Freshness, a.Directness, a.SourceClass, a.Corroboration.Sources)
}
