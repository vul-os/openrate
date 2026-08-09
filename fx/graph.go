// Package fx is openrate's pure core: the currency graph, the all-pairs
// snapshot materialized from it, and the accuracy model attached to every rate.
// It is importable by any Go program, imports nothing outside the standard
// library, opens no sockets, reads no environment, and starts no goroutines.
//
// Currencies are modelled as a graph rather than a single canonical base. Each
// known rate is a directed Edge; a conversion between any two currencies is the
// product of rates along the shortest path between them.
//
// This is deliberate: there is no "one true base". Sources publish in their own
// native base (ECB in EUR, SARB in ZAR, a crypto venue in USDT) and we keep
// those edges as-is. The materialized all-pairs Matrix is a *derived view*, so
// any currency — ZAR included — can be the presentation base for free.
package fx

import (
	"sort"
	"time"
)

// Edge is a single quoted rate: 1 unit of From equals Rate units of To, as
// published by Source at Time. The inverse edge is implied (1/Rate).
type Edge struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	Rate   float64   `json:"rate"`
	Source string    `json:"source"`
	Time   time.Time `json:"time"`
}

// Pair is a materialized conversion from one currency to another, carrying the
// provenance that matters for a freshness-focused API: how many hops the cross
// rate traversed and the oldest ("as of") timestamp on that path.
type Pair struct {
	Rate    float64   `json:"rate"`
	Hops    int       `json:"hops"`
	AsOf    time.Time `json:"as_of"`
	Path    []string  `json:"path"`
	Sources []string  `json:"sources"` // distinct sources of the edges on the path
	Legs    []Leg     `json:"legs"`    // each hop's actual rate + source (the calculation)
}

// Leg is one hop of a (possibly triangulated) conversion: 1 From = Rate To, as
// published by Source at Time. The product of all legs' rates is Pair.Rate —
// exactly, bit for bit: Materialize accumulates the path rate with the same
// left-to-right float64 multiplication, in the same order the legs are appended,
// so replaying the legs reproduces the identical double.
//
// That exactness is a property of the FULL-PRECISION values carried here, and it
// does not survive rounding. A display that prints each leg and the rate to,
// say, six decimals rounds each of them independently, and those roundings do
// not compose: multiplying the printed legs reproduces the printed rate only to
// within display rounding, and on real rates it usually differs in the last
// place. Anything that shows a reader the legs and the rate together must say
// so or show the residual — see fx/precision_test.go, which pins
// both the exact invariant and the bound on the display one.
type Leg struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	Rate   float64   `json:"rate"`
	Source string    `json:"source"`
	Time   time.Time `json:"time"`
}

// Quote is a single source's direct quote for an ordered pair, used to measure
// cross-source agreement (corroboration) for an exactly-quoted pair.
type Quote struct {
	Source string    `json:"source"`
	Rate   float64   `json:"rate"`
	Time   time.Time `json:"time"`
}

// Snapshot is an immutable all-pairs view built at BuiltAt. It is safe to share
// across goroutines once returned from Graph.Materialize.
type Snapshot struct {
	BuiltAt    time.Time                  `json:"built_at"`
	Currencies []string                   `json:"currencies"`
	matrix     map[string]map[string]Pair // matrix[from][to]
	direct     map[string][]Quote         // "FROM>TO" -> direct quotes (both directions)
}

// Lookup returns the materialized pair from->to, or ok=false if unreachable.
func (s *Snapshot) Lookup(from, to string) (Pair, bool) {
	if from == to {
		return Pair{Rate: 1, Hops: 0, AsOf: s.BuiltAt, Path: []string{from}}, true
	}
	row, ok := s.matrix[from]
	if !ok {
		return Pair{}, false
	}
	p, ok := row[to]
	return p, ok
}

// Has reports whether the snapshot knows this currency at all. Lookup cannot
// answer that: it treats from == to as the identity and returns a rate of 1 for
// any string, known or not.
func (s *Snapshot) Has(ccy string) bool {
	_, ok := s.matrix[ccy]
	return ok
}

// DirectQuotes returns every source's directly-quoted rate for from->to (inverse
// edges are folded in), used to assess cross-source agreement.
func (s *Snapshot) DirectQuotes(from, to string) []Quote {
	return s.direct[from+">"+to]
}

// Rebase returns every currency expressed against base: result[X] reads as
// "1 base = result[X].Rate units of X" (ECB/Frankfurter convention).
func (s *Snapshot) Rebase(base string) map[string]Pair {
	out := make(map[string]Pair, len(s.Currencies))
	for _, c := range s.Currencies {
		if c == base {
			continue
		}
		if p, ok := s.Lookup(base, c); ok {
			out[c] = p
		}
	}
	return out
}

// Graph is the mutable edge store. Edges are grouped by source so a refresh can
// atomically replace one source's contribution without disturbing the others.
type Graph struct {
	bySource map[string][]Edge
}

func NewGraph() *Graph {
	return &Graph{bySource: map[string][]Edge{}}
}

// Replace swaps in the full set of edges for a single source. Passing an empty
// slice clears that source (e.g. when a fetch returns nothing).
func (g *Graph) Replace(source string, edges []Edge) {
	g.bySource[source] = edges
}

// adjacency builds From -> []Edge including implied inverse edges, and a "FROM>TO"
// -> direct-quotes index (every source's direct rate for each ordered pair, both
// directions). When multiple edges connect the same ordered pair, the freshest
// wins (it sorts first), giving the "prefer the most recent quote" tie-break.
func (g *Graph) adjacency() (map[string][]Edge, []string, map[string][]Quote) {
	adj := map[string][]Edge{}
	direct := map[string][]Quote{}
	seen := map[string]bool{}
	add := func(e Edge) {
		adj[e.From] = append(adj[e.From], e)
		direct[e.From+">"+e.To] = append(direct[e.From+">"+e.To], Quote{Source: e.Source, Rate: e.Rate, Time: e.Time})
		seen[e.From] = true
		seen[e.To] = true
	}
	for _, edges := range g.bySource {
		for _, e := range edges {
			// A rate must be positive, finite AND bounded (see usable). `rate <= 0`
			// alone is not enough:
			// NaN and +Inf both fail that test (every comparison with NaN is false),
			// and strconv.ParseFloat accepts the literal strings "NaN"/"Inf" without
			// an error — so a feed emitting one (BIS already publishes literal NaN
			// for missing days) would put it in the graph. From there it poisons
			// every path through the edge and makes the JSON encoder abort
			// mid-response, handing the client a 200 with a truncated body. Refuse
			// it at the one chokepoint every edge passes through, exactly as
			// rates.Materialize refuses a non-finite observation.
			//
			// The magnitude band does the same job for values that are finite but
			// absurd (1e-306, 1e300): they survive every "is it a number" test and
			// then blow up the accuracy model computed from them.
			inv := 1 / e.Rate
			if !usable(e.Rate) || !usable(inv) {
				continue
			}
			add(e)
			add(Edge{From: e.To, To: e.From, Rate: inv, Source: e.Source, Time: e.Time})
		}
	}
	for node := range adj {
		neigh := adj[node]
		sort.Slice(neigh, func(i, j int) bool { return neigh[i].Time.After(neigh[j].Time) })
	}
	currencies := make([]string, 0, len(seen))
	for c := range seen {
		currencies = append(currencies, c)
	}
	sort.Strings(currencies)
	return adj, currencies, direct
}

// Materialize computes the all-pairs matrix via breadth-first search from every
// currency. BFS reaches each target by the fewest hops first, so a directly
// quoted pair (1 hop) always beats a triangulated one — exactly the
// "prefer direct, else shortest path, else freshest" rule.
func (g *Graph) Materialize(now time.Time) *Snapshot {
	adj, currencies, direct := g.adjacency()
	matrix := make(map[string]map[string]Pair, len(currencies))

	for _, start := range currencies {
		row := map[string]Pair{}
		row[start] = Pair{Rate: 1, Hops: 0, AsOf: now, Path: []string{start}}
		queue := []string{start}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			base := row[cur]
			for _, e := range adj[cur] {
				if _, done := row[e.To]; done {
					continue // first (shortest/freshest) wins
				}
				// Each leg is positive and finite (adjacency guarantees it), but the
				// product along a path can still overflow to +Inf or underflow to 0.
				// Such a path is not a rate, so skip it without marking the target
				// done — a longer, representable path may still reach it.
				rate := base.Rate * e.Rate
				if !usable(rate) {
					continue
				}
				asOf := base.AsOf
				if e.Time.Before(asOf) {
					asOf = e.Time
				}
				path := append(append([]string{}, base.Path...), e.To)
				leg := Leg{From: cur, To: e.To, Rate: e.Rate, Source: e.Source, Time: e.Time}
				row[e.To] = Pair{
					Rate:    rate,
					Hops:    base.Hops + 1,
					AsOf:    asOf,
					Path:    path,
					Sources: addDistinct(base.Sources, e.Source),
					Legs:    append(append([]Leg{}, base.Legs...), leg),
				}
				queue = append(queue, e.To)
			}
		}
		matrix[start] = row
	}
	return &Snapshot{BuiltAt: now, Currencies: currencies, matrix: matrix, direct: direct}
}

// A rate must be bounded in magnitude, not merely finite. float64 runs out to
// ~1.8e308, and admitting that entire span is what makes everything computed
// from a rate unprovable: two direct quotes for one pair at 1e-306 and 1e300
// send the accuracy model's (max-min)/min*10000 to +Inf, and round2's f*100
// overflows for any |f| >= ~1.8e306. Neither is a value encoding/json can emit,
// so a "rate" no feed on earth publishes takes down the response carrying it.
// Bounding the input is the only fix that holds for every expression downstream
// at once; patching each expression as it is found is a treadmill.
//
// The band is symmetric in log space, and that symmetry is load-bearing: every
// edge implies its inverse (1/Rate), so a band closed under reciprocal is one
// where admission can never be one-directional. [1e-18, 1e18] is closed under
// reciprocal exactly; the old guard was not, which is why an edge at
// math.SmallestNonzeroFloat64 was admitted while its inverse overflowed.
//
// 1e18 clears every rate a real feed publishes with room to spare:
//   - fiat spans roughly 1e-5..1e5 (IRR/USD at one end, USD/IRR at the other)
//   - crypto quotes reach 1e-8 per unit (satoshi), so a USD->sat rate is ~1e8
//   - the worst hyperinflation a modern feed carried, old ZWD, reached ~1e14
//   - 1e18 is itself the largest denomination ratio in live use: wei per ether
//
// So the tightest real case still sits four orders of magnitude inside the band,
// and anything outside it is a data error, not a price.
//
// The bound is what makes every downstream expression provably finite. For rates
// r in [1e-18, 1e18] over n distinct quotes, the worst case of each expression
// in Materialize, corroborate and round2 is:
//
//	1/r                  <= 1e18                  inverse edge
//	r_a * r_b            <= 1e36                  one BFS hop (checked before use)
//	sum of n quotes      <= n * 1e18              +Inf needs n > 1.8e290: unreachable
//	mean                 in [1e-18, 1e18]
//	(r-mean)^2           <= 4e36, so stdev <= 2e18
//	stdev/mean*10000     <= 2e18/1e-18*1e4 = 2e40 stdev_bps
//	(max-min)/min*10000  <= 1e18/1e-18*1e4 = 1e40 spread_bps
//	round2(2e40)         -> 2e40*100 = 2e42       the largest intermediate anywhere
//
// 2e42 is 266 orders of magnitude below the overflow point, so no arithmetic in
// the accuracy model can produce a token the JSON encoder rejects.
const (
	minRate = 1e-18
	maxRate = 1e18
)

// usable reports whether a rate is a positive, finite number inside the
// magnitude band above — the only kind that can be inverted, multiplied along a
// path, and then JSON-encoded alongside the accuracy model derived from it.
func usable(rate float64) bool {
	// The band subsumes the finiteness test it replaced: every comparison with
	// NaN is false, +Inf fails the upper bound, and -Inf (like 0 and any negative
	// rate) fails the lower one.
	return rate >= minRate && rate <= maxRate
}

func addDistinct(xs []string, x string) []string {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	out := make([]string, len(xs), len(xs)+1)
	copy(out, xs)
	return append(out, x)
}
