// Package rates models interest rates as flat, independent time series rather
// than a graph. Unlike FX (where currencies triangulate through shared bases),
// a policy rate and a reference rate do not compose into one another — each is a
// standalone series identified by a canonical ID and carrying its own history.
//
// A Series ID is "<AREA>.<type>[.<tenor>]", lower-cased and dotted, e.g.
// "us.policy", "xm.ref.estr", "za.ref.zaronia.3m". AREA is an ISO 3166 alpha-2
// country code, or a recognised area code such as "xm" (euro area). This shape
// is deliberately generic so any OSS consumer can publish their own series.
package rates

import (
	"math"
	"sort"
	"time"
)

// Type classifies what a series measures. These are open-ended conventions, not
// an enum the engine enforces — a source may emit any string — but consumers can
// rely on these for filtering.
const (
	TypePolicy    = "policy"    // central-bank policy / target rate
	TypeReference = "reference" // overnight risk-free / reference rate (SOFR, €STR, ZARONIA)
	TypeInterbank = "interbank" // term interbank offered rate (EURIBOR, JIBAR)
	TypeLending   = "lending"   // prime / lending rate
	TypeDeposit   = "deposit"   // deposit / savings rate
	TypeBond      = "bond"      // government bond yield
)

// Observation is a single interest-rate datapoint as published by one source.
// Value is in percent per annum (e.g. 7.25 means 7.25%). The source sets every
// field including Source, exactly as an FX source sets every field of an Edge.
type Observation struct {
	Series string    `json:"series"`          // canonical id, e.g. "us.policy"
	Area   string    `json:"area"`            // ISO 3166 alpha-2 / area code, e.g. "US", "XM"
	Type   string    `json:"type"`            // policy | reference | interbank | lending | deposit | bond
	Tenor  string    `json:"tenor,omitempty"` // "", "ON", "1W", "1M", "3M", "10Y"
	Name   string    `json:"name"`            // human label, e.g. "United States — policy rate"
	Value  float64   `json:"value"`           // percent per annum
	Date   time.Time `json:"date"`            // observation date
	Source string    `json:"source"`          // source name
}

// Quote is one source's most-recent value for a series, used to measure
// cross-source agreement (corroboration).
type Quote struct {
	Source string    `json:"source"`
	Value  float64   `json:"value"`
	Date   time.Time `json:"date"`
}

// Point is one observation on the headline series history (timeseries view).
type Point struct {
	Date   time.Time `json:"date"`
	Value  float64   `json:"value"`
	Source string    `json:"source"`
}

// Series is the materialized view of one canonical series: the headline latest
// value (from the most authoritative, then freshest source), the per-source
// latest quotes behind it, and the headline source's full history.
type Series struct {
	Series  string    `json:"series"`
	Area    string    `json:"area"`
	Type    string    `json:"type"`
	Tenor   string    `json:"tenor,omitempty"`
	Name    string    `json:"name"`
	Value   float64   `json:"value"` // headline latest value
	Date    time.Time `json:"date"`  // date of the headline value
	Source  string    `json:"source"`
	Sources []string  `json:"sources"` // every source contributing to this series
	Latest  []Quote   `json:"latest"`  // each source's most-recent value (corroboration)
	History []Point   `json:"history"` // headline source's observations, ascending by date
}

// Snapshot is an immutable, all-series view built at BuiltAt. Safe to share
// across goroutines once returned from Book.Materialize.
type Snapshot struct {
	BuiltAt time.Time         `json:"built_at"`
	Series  map[string]Series `json:"series"`
	ids     []string
}

// IDs returns the sorted list of series IDs in the snapshot.
func (s *Snapshot) IDs() []string { return s.ids }

// Lookup returns the materialized series by ID.
func (s *Snapshot) Lookup(id string) (Series, bool) {
	v, ok := s.Series[id]
	return v, ok
}

// SourceRank ranks source authority for interest rates (higher = more
// authoritative). The issuing central bank's own feed is the gold standard;
// official multilateral aggregators (BIS, FRED, OECD, IMF, ECB portals) are a
// notch below; commercial aggregators below that. Unlisted sources rank 0.
//
// Keeping the rank here (rather than in the quality package) lets Materialize
// pick the headline source without importing quality, and lets quality import
// it without a cycle.
var SourceRank = map[string]int{
	"sarbrates": 4, // South African Reserve Bank, direct issuer of ZARONIA
	"bis":       3, // BIS central bank policy rates — official multilateral aggregator
	"fred":      3, // Federal Reserve (St. Louis) data portal
	"ecbrates":  3, // ECB Data Portal
}

// Rank returns the authority rank of a source (0 if unknown).
func Rank(source string) int { return SourceRank[source] }

// Book is the mutable observation store. Observations are grouped by source so a
// refresh can atomically replace one source's contribution without disturbing
// the others — exactly mirroring the FX graph's per-source Replace.
type Book struct {
	bySource map[string][]Observation
}

func New() *Book { return &Book{bySource: map[string][]Observation{}} }

// Replace swaps in the full set of observations for one source. An empty slice
// clears that source.
func (b *Book) Replace(source string, obs []Observation) {
	b.bySource[source] = obs
}

// Materialize folds every source's observations into the all-series snapshot.
// For each series it picks a headline source (highest authority, then most
// recent observation), records each source's latest value for corroboration,
// and attaches the headline source's full history sorted ascending by date.
func (b *Book) Materialize(now time.Time) *Snapshot {
	// Group observations by series, then by source.
	type group struct {
		meta  Observation // any observation, for the series metadata
		bySrc map[string][]Observation
	}
	groups := map[string]*group{}
	for _, obs := range b.bySource {
		for _, o := range obs {
			// Skip empty series and non-finite values: a NaN/Inf would survive
			// JSON marshaling as an error and blank the whole API response, so the
			// model refuses to admit one regardless of which source emitted it.
			if o.Series == "" || math.IsNaN(o.Value) || math.IsInf(o.Value, 0) {
				continue
			}
			g := groups[o.Series]
			if g == nil {
				g = &group{meta: o, bySrc: map[string][]Observation{}}
				groups[o.Series] = g
			}
			g.bySrc[o.Source] = append(g.bySrc[o.Source], o)
		}
	}

	series := make(map[string]Series, len(groups))
	ids := make([]string, 0, len(groups))
	for id, g := range groups {
		ids = append(ids, id)

		// Per-source latest value (corroboration) and headline selection.
		var latest []Quote
		var headSrc string
		var headObs Observation
		headRank, headDate := -1, time.Time{}
		var allSources []string
		for src, obs := range g.bySrc {
			allSources = append(allSources, src)
			newest := obs[0]
			for _, o := range obs[1:] {
				if o.Date.After(newest.Date) {
					newest = o
				}
			}
			latest = append(latest, Quote{Source: src, Value: newest.Value, Date: newest.Date})

			r := Rank(src)
			if r > headRank || (r == headRank && newest.Date.After(headDate)) {
				headRank, headDate = r, newest.Date
				headSrc, headObs = src, newest
			}
		}
		sort.Strings(allSources)
		sort.Slice(latest, func(i, j int) bool { return latest[i].Source < latest[j].Source })

		// Headline source history, ascending by date (deduped by date, freshest wins).
		hist := dedupeByDate(g.bySrc[headSrc])

		series[id] = Series{
			Series:  id,
			Area:    g.meta.Area,
			Type:    g.meta.Type,
			Tenor:   g.meta.Tenor,
			Name:    headObs.Name,
			Value:   headObs.Value,
			Date:    headObs.Date,
			Source:  headSrc,
			Sources: allSources,
			Latest:  latest,
			History: hist,
		}
	}
	sort.Strings(ids)
	return &Snapshot{BuiltAt: now, Series: series, ids: ids}
}

// dedupeByDate returns one point per calendar date (latest observation wins),
// sorted ascending by date.
func dedupeByDate(obs []Observation) []Point {
	byDay := map[string]Observation{}
	for _, o := range obs {
		key := o.Date.Format("2006-01-02")
		if cur, ok := byDay[key]; !ok || o.Date.After(cur.Date) {
			byDay[key] = o
		}
	}
	out := make([]Point, 0, len(byDay))
	for _, o := range byDay {
		out = append(out, Point{Date: o.Date, Value: o.Value, Source: o.Source})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out
}
