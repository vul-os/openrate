package serve

// The read endpoints over the current snapshot. Every rate carries its
// provenance (hops, as_of, age) so a freshness-focused consumer can see exactly
// how stale each number is — the most valuable thing such an API can surface,
// especially across weekends when the fiat market is closed.

import (
	"bytes"
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

	// NOTE — an unknown base is NOT rejected here, and that is a standing
	// inconsistency rather than an oversight. Rebase yields nothing for a base
	// the snapshot does not know, so ?base=ZZZ answers 200 with `"rates":{}`,
	// which a caller checking only the status code reads as "no rates
	// available". [openrate.Engine.Rates] and the C ABI both return
	// ErrUnknownBase for the same input.
	//
	// It stays because it is a published contract, not an accident: sdks/go,
	// sdks/python and sdks/deno each document the difference in their README,
	// two of their example programs demonstrate it deliberately, and the ABI's
	// own comment cites the HTTP behaviour as the thing it is departing from.
	// Aligning it is a wire change that has to land together with those, which
	// makes it a decision for the API contract and not a fix to make quietly
	// inside the serving layer. TestRatesUnknownBaseKeepsItsDocumentedShape
	// pins it so it cannot drift either way unnoticed.
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
	amount, err := parseAmount(r.URL.Query().Get("amount"))
	if err != nil {
		writeAPIError(w, err)
		return
	}

	now := s.now().UTC()
	snap := s.engine.Snapshot()

	// A currency the snapshot does not know is an unknown pair, including when
	// both sides are the same unknown code. Describe cannot catch that on its
	// own: Snapshot.Lookup answers the identity for from == to whatever the
	// string is, so ?from=NOTACCY&to=NOTACCY used to return 200 with rate 1 and
	// a quality grade attached — an invented answer about a currency that does
	// not exist, in the shape of a real one. USD->USD stays exactly as it was:
	// the code is known, so the identity is a true statement about it.
	if !snap.Has(from) || !snap.Has(to) {
		writeAPIError(w, fx.ErrUnknownPair)
		return
	}

	c, err := fx.Describe(snap, from, to, amount, now)
	if err != nil {
		// Non-finite amounts and unrepresentable products both parse fine but
		// make the JSON encoder fail mid-write, leaving the client with a 200
		// and a truncated body. Fail cleanly instead.
		writeAPIError(w, err)
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

// parseAmount reads the ?amount= parameter. Three cases the endpoint used to
// answer identically, and must not:
//
//   - ABSENT (or empty): the documented default of 1, so /convert?from=X&to=Y
//     keeps returning the plain exchange rate.
//   - UNPARSEABLE ("notanumber"): also 1. That is the endpoint's documented,
//     long-standing behaviour and callers depend on it, so it stays.
//   - OUT OF RANGE ("1e999"): rejected. ParseFloat returns ±Inf together with
//     strconv.ErrRange for a literal too large to represent, and the old code
//     tested only `err == nil` — so the ±Inf was thrown away along with the
//     error and the request silently converted 1.0 instead. A caller asking to
//     convert 1e999 units got a successful-looking answer about one unit. The
//     error is fx.ErrInvalidAmount, exactly as for the ?amount=Inf that parses
//     to the same value without an error, and it is a 400.
func parseAmount(raw string) (float64, error) {
	if raw == "" {
		return 1, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if errors.Is(err, strconv.ErrRange) {
		return 0, fx.ErrInvalidAmount
	}
	if err != nil {
		return 1, nil
	}
	return v, nil
}

// writeAPIError is the one error path for the read endpoints, so a refusal
// cannot drift in shape between them: an unknown pair is a 404, every other
// refusal is a 400, and the body is the sentinel's own text — the same string
// fx exports and the C ABI wraps, so a client can match on it.
func writeAPIError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, fx.ErrUnknownPair) {
		status = http.StatusNotFound
	}
	http.Error(w, `{"error":"`+err.Error()+`"}`, status)
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
// handleReady answers whether the engine can actually serve a conversion yet.
//
// /healthz is liveness: it says the process is up, and it answers the instant
// the listener binds — before any source has been fetched. That is the correct
// contract for a liveness probe and the wrong one for a client deciding when
// to send its first request, and the gap is not theoretical: every managed
// sidecar in sdks/ was written against /healthz, saw a 200, converted
// immediately, got {"error":"unknown or unreachable currency pair"} for every
// pair, and exited 0. A false green that also disguises itself as a bad
// currency code.
//
// Readiness is "the snapshot has currencies in it". Not ready is a 503 that
// carries the per-source outcomes, so a caller can print WHY it is not ready
// (ecb: connection refused) instead of a bare timeout.
//
// It sits outside /api/ deliberately: guard() applies the rate limiter only to
// /api/ paths, so a client polling this while it waits cannot burn the budget
// it is waiting to use. Polling /api/v1/meta — which is what the SDKs had to
// do without this — does exactly that.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	snap := s.engine.Snapshot()
	n := len(snap.Currencies)
	body := map[string]any{
		"ready":      n > 0,
		"currencies": n,
		"built_at":   snap.BuiltAt,
		"sources":    s.status(),
	}
	if n == 0 {
		body["reason"] = "no rates yet: no source has returned a usable quote"
		s.writeJSONStatus(w, http.StatusServiceUnavailable, body)
		return
	}
	s.writeJSON(w, body)
}

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
	s.writeJSONStatus(w, http.StatusOK, v)
}

// writeJSONStatus is writeJSON with an explicit code. Every JSON body this
// server emits goes through here so the encoding cannot drift by status: the
// first cut of /readyz hand-rolled its 503 and shipped an indented 200 next to
// a compact 503, which every substring-scanning client then had to tolerate.
func (s *Server) writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", s.cors)
	// When the allowed origin is a specific host (not the wildcard), the CORS
	// response varies by request Origin, so caches must key on it — otherwise a
	// cached response for one origin could be served to another.
	if s.cors != "*" {
		w.Header().Set("Vary", "Origin")
	}
	// Encode into a buffer BEFORE the status line goes out. Encoding straight
	// to the ResponseWriter writes the header first and then discards the
	// encoder's error, so a value json cannot represent — a +Inf that reached
	// a quality figure, say — published a 200 with an empty body. A client
	// cannot tell that apart from "no data", and slipscan's OpenRate client
	// carries defensive decoding specifically because of this shape.
	//
	// A body that cannot be encoded is a server fault, so it is a 500 with a
	// diagnosable message rather than a successful-looking void.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"response could not be encoded"}` + "\n"))
		return
	}
	if code != http.StatusOK {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	if code != http.StatusOK {
		w.WriteHeader(code)
	}
	_, _ = w.Write(buf.Bytes())
}
