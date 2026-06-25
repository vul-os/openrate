// Package ratesapi exposes read endpoints over the current interest-rate
// snapshot. Every series carries its provenance (source, age, history) and a
// confidence grade, mirroring the FX API's freshness-first philosophy. Routes
// live under /api/v1/interest/ so they sit alongside the FX API on one server.
package ratesapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/vul-os/openrate/internal/ratequality"
	"github.com/vul-os/openrate/internal/rates"
	"github.com/vul-os/openrate/internal/ratestore"
)

type Server struct {
	Store *ratestore.Store
}

func New(st *ratestore.Store) *Server { return &Server{Store: st} }

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/interest/rates", s.handleRates)
	mux.HandleFunc("/api/v1/interest/series", s.handleSeries)
	mux.HandleFunc("/api/v1/interest/meta", s.handleMeta)
}

type quoteView struct {
	Source string    `json:"source"`
	Value  float64   `json:"value"`
	Date   time.Time `json:"date"`
	AgeSec float64   `json:"age_sec"`
}

// rateView is the headline (latest) view of a series — no history, for the
// all-series listing.
type rateView struct {
	Series  string                 `json:"series"`
	Area    string                 `json:"area"`
	Type    string                 `json:"type"`
	Tenor   string                 `json:"tenor,omitempty"`
	Name    string                 `json:"name"`
	Value   float64                `json:"value"`
	Date    time.Time              `json:"date"`
	AgeSec  float64                `json:"age_sec"`
	Source  string                 `json:"source"`
	Sources []string               `json:"sources"`
	Quality ratequality.Assessment `json:"quality"`
	Quotes  []quoteView            `json:"quotes"`
}

func headline(s rates.Series, now time.Time) rateView {
	var quotes []quoteView
	for _, q := range s.Latest {
		quotes = append(quotes, quoteView{Source: q.Source, Value: q.Value, Date: q.Date, AgeSec: now.Sub(q.Date).Seconds()})
	}
	return rateView{
		Series:  s.Series,
		Area:    s.Area,
		Type:    s.Type,
		Tenor:   s.Tenor,
		Name:    s.Name,
		Value:   s.Value,
		Date:    s.Date,
		AgeSec:  now.Sub(s.Date).Seconds(),
		Source:  s.Source,
		Sources: s.Sources,
		Quality: ratequality.Assess(s, now),
		Quotes:  quotes,
	}
}

// GET /api/v1/interest/rates[?area=US][&type=policy]
// Returns the headline (latest) value for every series, filterable by area/type.
func (s *Server) handleRates(w http.ResponseWriter, r *http.Request) {
	area := upper(r.URL.Query().Get("area"))
	typ := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	snap := s.Store.Snapshot()
	now := time.Now().UTC()

	out := make([]rateView, 0, len(snap.Series))
	for _, id := range snap.IDs() {
		v := snap.Series[id]
		if area != "" && v.Area != area {
			continue
		}
		if typ != "" && v.Type != typ {
			continue
		}
		out = append(out, headline(v, now))
	}
	writeJSON(w, map[string]any{
		"built_at": snap.BuiltAt,
		"count":    len(out),
		"rates":    out,
	})
}

// GET /api/v1/interest/series?id=us.policy
// Returns one series with its full headline-source history (timeseries).
func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("id")))
	if id == "" {
		http.Error(w, `{"error":"missing series id"}`, http.StatusBadRequest)
		return
	}
	snap := s.Store.Snapshot()
	v, ok := snap.Lookup(id)
	if !ok {
		http.Error(w, `{"error":"unknown series"}`, http.StatusNotFound)
		return
	}
	now := time.Now().UTC()
	writeJSON(w, map[string]any{
		"built_at": snap.BuiltAt,
		"series":   headline(v, now),
		"history":  v.History,
	})
}

// GET /api/v1/interest/meta -> sources status, areas, and the series catalogue.
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	snap := s.Store.Snapshot()
	areaSet := map[string]bool{}
	type catEntry struct {
		Series string `json:"series"`
		Area   string `json:"area"`
		Type   string `json:"type"`
		Tenor  string `json:"tenor,omitempty"`
		Name   string `json:"name"`
	}
	catalogue := make([]catEntry, 0, len(snap.Series))
	for _, id := range snap.IDs() {
		v := snap.Series[id]
		areaSet[v.Area] = true
		catalogue = append(catalogue, catEntry{Series: v.Series, Area: v.Area, Type: v.Type, Tenor: v.Tenor, Name: v.Name})
	}
	areas := make([]string, 0, len(areaSet))
	for a := range areaSet {
		areas = append(areas, a)
	}
	sort.Strings(areas)

	writeJSON(w, map[string]any{
		"built_at":   snap.BuiltAt,
		"areas":      areas,
		"area_count": len(areas),
		"series":     catalogue,
		"sources":    s.Store.Status(),
	})
}

func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
