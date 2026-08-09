package serve

// The read endpoints over the current snapshot. Every rate carries its
// provenance (hops, as_of, age) so a freshness-focused consumer can see exactly
// how stale each number is — the most valuable thing such an API can surface,
// especially across weekends when the fiat market is closed.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/fxsource"
)

// rateView is the wire shape of a rate. It is a projection of [fx.Conversion]
// and nothing more: every field is copied straight across, so the number an
// HTTP client sees is the number a library caller gets, to the bit. The type
// exists only because the wire has always nested this object under "rate" in
// the convert response, without the amount/result fields around it.
type rateView struct {
	Rate    float64          `json:"rate"`
	Hops    int              `json:"hops"`
	AsOf    time.Time        `json:"as_of"`
	AgeSec  float64          `json:"age_sec"`
	Path    []string         `json:"path"`
	Sources []string         `json:"sources"`
	Quality fx.Assessment    `json:"quality"`
	Legs    []legView        `json:"legs"`   // each hop's actual rate + source (the calculation)
	Quotes  []fx.SourceQuote `json:"quotes"` // per-source direct quotes behind the number
}

// legView ages each leg for the wire. fx.Leg carries the quote's timestamp,
// which is the more useful thing for a library caller to hold; the API has
// always published the age instead, so the conversion happens here — on the
// time, never on the rate, which is copied across untouched.
type legView struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Rate   float64 `json:"rate"`
	Source string  `json:"source"`
	AgeSec float64 `json:"age_sec"`
}

func viewOf(c fx.Conversion, now time.Time) rateView {
	var legs []legView
	for _, l := range c.Legs {
		legs = append(legs, legView{
			From: l.From, To: l.To, Rate: l.Rate, Source: l.Source,
			AgeSec: now.Sub(l.Time).Seconds(),
		})
	}
	return rateView{
		Rate:    c.Rate,
		Hops:    c.Hops,
		AsOf:    c.AsOf,
		AgeSec:  c.AgeSec,
		Path:    c.Path,
		Sources: c.Sources,
		Quality: c.Quality,
		Legs:    legs,
		Quotes:  c.Quotes,
	}
}

// GET /api/v1/rates?base=ZAR  -> { base, built_at, rates: { CCY: rateView } }
// rates[X].rate reads as "1 base = rate units of X".
func (s *Server) handleRates(w http.ResponseWriter, r *http.Request) {
	base := s.base(r)
	// One snapshot for the whole response: a refresh landing mid-loop would
	// otherwise mix two books into one payload with a single built_at on top.
	snap := s.engine.Snapshot()
	now := s.now().UTC()

	rates := map[string]rateView{}
	for ccy := range snap.Rebase(base) {
		c, err := fx.Describe(snap, base, ccy, 1, now)
		if err != nil {
			// Rebase only offers reachable pairs, so this is unreachable in
			// practice; skipping is the same degradation Rebase already applies
			// to a pair whose rate is not representable.
			continue
		}
		rates[ccy] = viewOf(c, now)
	}
	s.writeJSON(w, map[string]any{
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
		from = s.engine.DefaultBase()
	}
	if to == "" {
		to = s.engine.DefaultBase()
	}
	amount := 1.0
	if a := r.URL.Query().Get("amount"); a != "" {
		// Genuinely unparseable input keeps the historical default of 1.0; a
		// parseable but non-finite one is refused below by Describe.
		if v, err := strconv.ParseFloat(a, 64); err == nil {
			amount = v
		}
	}

	now := s.now().UTC()
	c, err := fx.Describe(s.engine.Snapshot(), from, to, amount, now)
	if err != nil {
		// Non-finite amounts and unrepresentable products both parse fine but
		// make the JSON encoder fail mid-write, leaving the client with a 200
		// and a truncated body. Fail cleanly instead.
		status := http.StatusBadRequest
		if errors.Is(err, fx.ErrUnknownPair) {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, status)
		return
	}

	s.writeJSON(w, map[string]any{
		"from":   c.From,
		"to":     c.To,
		"amount": c.Amount,
		"result": c.Result,
		"rate":   viewOf(c, now),
	})
}

// GET /api/v1/meta -> sources + freshness + currency list
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	snap := s.engine.Snapshot()
	s.writeJSON(w, map[string]any{
		"default_base": s.engine.DefaultBase(),
		"built_at":     snap.BuiltAt,
		"currencies":   snap.Currencies,
		"sources":      s.status(),
	})
}

// status is the source list for /meta. A server over an engine nobody refreshes
// reports an empty list rather than null: there are no sources, which is a fact
// about this deployment and not a missing field.
func (s *Server) status() []fxsource.Status {
	if s.opts.Status == nil {
		return []fxsource.Status{}
	}
	if st := s.opts.Status(); st != nil {
		return st
	}
	return []fxsource.Status{}
}

func (s *Server) base(r *http.Request) string {
	if b := upper(r.URL.Query().Get("base")); b != "" {
		return b
	}
	return s.engine.DefaultBase()
}

func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", s.cors)
	// When the allowed origin is a specific host (not the wildcard), the CORS
	// response varies by request Origin, so caches must key on it — otherwise a
	// cached response for one origin could be served to another.
	if s.cors != "*" {
		w.Header().Set("Vary", "Origin")
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
