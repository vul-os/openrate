// Package api exposes the read endpoints over the current snapshot. Every rate
// carries its provenance (hops, as_of, age) so a freshness-focused consumer can
// see exactly how stale each number is — the most valuable thing such an API
// can surface, especially across weekends when the fiat market is closed.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vul-os/openrate/internal/graph"
	"github.com/vul-os/openrate/internal/quality"
	"github.com/vul-os/openrate/internal/store"
)

type Server struct {
	Store       *store.Store
	DefaultBase string // ZAR — openrate's anchor
}

func New(st *store.Store, defaultBase string) *Server {
	return &Server{Store: st, DefaultBase: strings.ToUpper(defaultBase)}
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/rates", s.handleRates)
	mux.HandleFunc("/api/v1/convert", s.handleConvert)
	mux.HandleFunc("/api/v1/meta", s.handleMeta)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
}

type quoteView struct {
	Source string  `json:"source"`
	Rate   float64 `json:"rate"`
	AgeSec float64 `json:"age_sec"`
}

type legView struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Rate   float64 `json:"rate"`
	Source string  `json:"source"`
	AgeSec float64 `json:"age_sec"`
}

type rateView struct {
	Rate    float64            `json:"rate"`
	Hops    int                `json:"hops"`
	AsOf    time.Time          `json:"as_of"`
	AgeSec  float64            `json:"age_sec"`
	Path    []string           `json:"path"`
	Sources []string           `json:"sources"`
	Quality quality.Assessment `json:"quality"`
	Legs    []legView          `json:"legs"`   // each hop's actual rate + source (the calculation)
	Quotes  []quoteView        `json:"quotes"` // per-source direct quotes behind the number
}

// view builds a rate view; from/to scope the quality assessment (currency
// caveats + cross-source corroboration) and the per-source quotes for the pair.
func view(snap *graph.Snapshot, from, to string, p graph.Pair, now time.Time) rateView {
	var quotes []quoteView
	for _, q := range snap.DirectQuotes(from, to) {
		quotes = append(quotes, quoteView{Source: q.Source, Rate: q.Rate, AgeSec: now.Sub(q.Time).Seconds()})
	}
	var legs []legView
	for _, l := range p.Legs {
		legs = append(legs, legView{From: l.From, To: l.To, Rate: l.Rate, Source: l.Source, AgeSec: now.Sub(l.Time).Seconds()})
	}
	return rateView{
		Rate:    p.Rate,
		Hops:    p.Hops,
		AsOf:    p.AsOf,
		AgeSec:  now.Sub(p.AsOf).Seconds(),
		Path:    p.Path,
		Sources: p.Sources,
		Quality: quality.Assess(from, to, p, snap.DirectQuotes(from, to), now),
		Legs:    legs,
		Quotes:  quotes,
	}
}

// GET /api/v1/rates?base=ZAR  -> { base, built_at, rates: { CCY: rateView } }
// rates[X].rate reads as "1 base = rate units of X".
func (s *Server) handleRates(w http.ResponseWriter, r *http.Request) {
	base := s.base(r)
	snap := s.Store.Snapshot()
	now := time.Now().UTC()
	rates := map[string]rateView{}
	for ccy, p := range snap.Rebase(base) {
		rates[ccy] = view(snap, base, ccy, p, now)
	}
	writeJSON(w, map[string]any{
		"base":     base,
		"built_at": snap.BuiltAt,
		"rates":    rates,
	})
}

// GET /api/v1/convert?from=USD&to=ZAR&amount=100
func (s *Server) handleConvert(w http.ResponseWriter, r *http.Request) {
	from := upper(r.URL.Query().Get("from"))
	to := upper(r.URL.Query().Get("to"))
	if from == "" {
		from = s.DefaultBase
	}
	if to == "" {
		to = s.DefaultBase
	}
	amount := 1.0
	if a := r.URL.Query().Get("amount"); a != "" {
		if v, err := strconv.ParseFloat(a, 64); err == nil {
			amount = v
		}
	}
	snap := s.Store.Snapshot()
	p, ok := snap.Lookup(from, to)
	if !ok {
		http.Error(w, `{"error":"unknown or unreachable currency pair"}`, http.StatusNotFound)
		return
	}
	now := time.Now().UTC()
	writeJSON(w, map[string]any{
		"from":   from,
		"to":     to,
		"amount": amount,
		"result": amount * p.Rate,
		"rate":   view(snap, from, to, p, now),
	})
}

// GET /api/v1/meta -> sources + freshness + currency list
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	snap := s.Store.Snapshot()
	writeJSON(w, map[string]any{
		"default_base": s.DefaultBase,
		"built_at":     snap.BuiltAt,
		"currencies":   snap.Currencies,
		"sources":      s.Store.Status(),
	})
}

func (s *Server) base(r *http.Request) string {
	if b := upper(r.URL.Query().Get("base")); b != "" {
		return b
	}
	return s.DefaultBase
}

func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
