// Package graph models currencies as a graph rather than a single canonical
// base. Each known rate is a directed Edge; a conversion between any two
// currencies is the product of rates along the shortest path between them.
//
// This is deliberate: there is no "one true base". Sources publish in their own
// native base (ECB in EUR, SARB in ZAR, a crypto venue in USDT) and we keep
// those edges as-is. The materialized all-pairs Matrix is a *derived view*, so
// any currency — ZAR included — can be the presentation base for free.
package graph

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
	Rate  float64   `json:"rate"`
	Hops  int       `json:"hops"`
	AsOf  time.Time `json:"as_of"`
	Path  []string  `json:"path"`
}

// Snapshot is an immutable all-pairs view built at BuiltAt. It is safe to share
// across goroutines once returned from Graph.Materialize.
type Snapshot struct {
	BuiltAt    time.Time                  `json:"built_at"`
	Currencies []string                   `json:"currencies"`
	matrix     map[string]map[string]Pair // matrix[from][to]
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

func New() *Graph {
	return &Graph{bySource: map[string][]Edge{}}
}

// Replace swaps in the full set of edges for a single source. Passing an empty
// slice clears that source (e.g. when a fetch returns nothing).
func (g *Graph) Replace(source string, edges []Edge) {
	g.bySource[source] = edges
}

// adjacency builds From -> []Edge including implied inverse edges. When multiple
// edges connect the same ordered pair, the freshest wins (it sorts first), which
// gives us the "prefer the most recent quote" tie-break among equal-length paths.
func (g *Graph) adjacency() (map[string][]Edge, []string) {
	adj := map[string][]Edge{}
	seen := map[string]bool{}
	add := func(e Edge) {
		adj[e.From] = append(adj[e.From], e)
		seen[e.From] = true
		seen[e.To] = true
	}
	for _, edges := range g.bySource {
		for _, e := range edges {
			if e.Rate <= 0 {
				continue
			}
			add(e)
			add(Edge{From: e.To, To: e.From, Rate: 1 / e.Rate, Source: e.Source, Time: e.Time})
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
	return adj, currencies
}

// Materialize computes the all-pairs matrix via breadth-first search from every
// currency. BFS reaches each target by the fewest hops first, so a directly
// quoted pair (1 hop) always beats a triangulated one — exactly the
// "prefer direct, else shortest path, else freshest" rule.
func (g *Graph) Materialize(now time.Time) *Snapshot {
	adj, currencies := g.adjacency()
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
				asOf := base.AsOf
				if e.Time.Before(asOf) {
					asOf = e.Time
				}
				path := append(append([]string{}, base.Path...), e.To)
				row[e.To] = Pair{Rate: base.Rate * e.Rate, Hops: base.Hops + 1, AsOf: asOf, Path: path}
				queue = append(queue, e.To)
			}
		}
		matrix[start] = row
	}
	return &Snapshot{BuiltAt: now, Currencies: currencies, matrix: matrix}
}
