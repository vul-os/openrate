package abi

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/fxsource"
)

// engineConfig is openrate_new's config document.
type engineConfig struct {
	Base  string `json:"base"`
	Quiet bool   `json:"quiet"`
}

// engineObj is one Engine behind a handle, plus the refreshers built over it.
//
// It knows about its refreshers for two reasons, both of them about not
// surprising a host: "meta" reports their source status the way GET
// /api/v1/meta does, and closing the engine stops them, so a ticker cannot
// outlive the handle that owns it.
type engineObj struct {
	e *openrate.Engine
	// handle is this engine's own registry key, known before the object becomes
	// reachable. adopt needs it to name the engine in the "not open" error a
	// host gets when it builds a refresher over an engine it is closing.
	handle uint64
	// now is the response clock, matching serve.Options.Now. One instant per
	// response, so every rate in a /rates payload is aged against the same
	// moment. Tests pin it.
	now func() time.Time

	mu sync.Mutex
	// closed is terminal. It is what makes adopt and close mutually exclusive
	// rather than merely mutex-protected: whichever runs second must be able to
	// see that the other already happened.
	closed     bool
	refreshers []*refresherObj
}

func (o *engineObj) kindName() string { return "engine" }

// adopt publishes r's handle in the registry AND appends r to this engine's
// child list, as one critical section, or does neither.
//
// Both halves have to be one step. A refresher in the registry but not in the
// list is a handle no close can reach: the engine has already snapshotted its
// children, so closing the engine will not take it, and the host does not know
// the number to close it with because openrate_refresher_new has not returned
// yet. openrate_open_handles() then never comes back to zero, which is the one
// assertion that entry point exists to support.
//
// LOCK ORDER: this takes the registry lock (inside install) while holding o.mu.
// That is the package's declared order — see the comment above the registry in
// abi.go — and it is safe because nothing in the registry ever calls back into
// an object.
func (o *engineObj) adopt(r *refresherObj) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		// The engine was closed while this refresher was being constructed.
		// Same error the host would have got had close won the race a moment
		// earlier and made the lookup in NewRefresher fail — because from the
		// host's side it is the same event.
		return errHandleNotOpen(o.handle)
	}
	install(r.handle, r)
	o.refreshers = append(o.refreshers, r)
	return nil
}

// detach removes r from the child list. A refresher the host closed itself must
// stop being reported by "meta" — a fetch status from an object whose handle is
// gone is a fact about nothing — and a long-lived engine that a host opens and
// closes refreshers over must not accumulate one dead *refresherObj per cycle.
//
// It is called from refresherObj.close, which must NOT hold its own lock: the
// order is engineObj.mu before refresherObj.mu.
func (o *engineObj) detach(r *refresherObj) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i, x := range o.refreshers {
		if x == r {
			o.refreshers = append(o.refreshers[:i], o.refreshers[i+1:]...)
			return
		}
	}
}

// statuses is what /api/v1/meta calls its "sources" field: the fetch outcome of
// every source, from every Refresher built over this engine, in construction
// order. An engine nobody refreshes reports an empty list — no sources is a
// fact about this host, not a missing field.
func (o *engineObj) statuses() []fxsource.Status {
	o.mu.Lock()
	rs := append([]*refresherObj(nil), o.refreshers...)
	o.mu.Unlock()
	out := []fxsource.Status{}
	for _, r := range rs {
		out = append(out, r.r.Status()...)
	}
	return out
}

// close stops every refresher this engine owns and unregisters their handles.
// The Engine itself holds no resources — it is a snapshot behind a mutex — so
// there is nothing else to release.
//
// Taking each refresher out of the registry first means a host that closes the
// engine and then closes a refresher does not run the teardown twice, and a
// host that closes the engine and then USES a refresher gets "handle is not
// open" rather than a ticker feeding an engine nobody can reach.
//
// o.mu is released before any of that: r.close reaches back into detach, and
// take reaches into the registry, so holding the engine's lock across the loop
// would invert the package's lock order.
func (o *engineObj) close() {
	o.mu.Lock()
	// Set BEFORE releasing the lock, in the same critical section that empties
	// the list: a refresher being adopted concurrently either gets in before
	// this and appears in rs, or finds closed and is never registered at all.
	o.closed = true
	rs := o.refreshers
	o.refreshers = nil
	o.mu.Unlock()
	for _, r := range rs {
		if _, ok := take(r.handle); ok {
			r.close()
		}
	}
}

func (o *engineObj) call(method string, req []byte) ([]byte, error) {
	switch method {
	case "convert":
		return o.convert(req)
	case "rates":
		return o.rates(req)
	case "meta":
		return o.meta(req)
	case "load":
		return o.load(req)
	default:
		return nil, fmt.Errorf("openrate: unknown engine method %q (have: convert, rates, meta, load)", method)
	}
}

// --- the wire shapes -------------------------------------------------------
//
// These mirror serve/api.go field for field, on purpose: the JSON that crosses
// the C boundary is the JSON the HTTP API already publishes and already tests,
// so a binding written against one works against the other and a reader learns
// one document format.
//
// They are DUPLICATED rather than imported, because importing serve would drag
// the HTTP server and the embedded web console into every shared library
// openrate ships — the exact cost the Engine/Refresher split exists to avoid.
// Duplication of a wire shape is how mock-and-core drift starts, so it is
// pinned: TestWireParityWithTheHTTPAPI in wire_parity_test.go builds one engine,
// asks it the same question over this ABI and over serve's real handler, and
// requires the two documents to be equal. If serve adds a field and this does
// not, that test fails.

type rateView struct {
	Rate    float64          `json:"rate"`
	Hops    int              `json:"hops"`
	AsOf    time.Time        `json:"as_of"`
	AgeSec  float64          `json:"age_sec"`
	Path    []string         `json:"path"`
	Sources []string         `json:"sources"`
	Quality fx.Assessment    `json:"quality"`
	Legs    []legView        `json:"legs"`
	Quotes  []fx.SourceQuote `json:"quotes"`
}

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

// --- convert ---------------------------------------------------------------

// convertRequest is the FFI form of GET /api/v1/convert's query string. An
// omitted currency means the engine's default base and an omitted amount means
// 1, both exactly as the HTTP handler does it.
type convertRequest struct {
	From   string   `json:"from"`
	To     string   `json:"to"`
	Amount *float64 `json:"amount"`
}

func (o *engineObj) convert(req []byte) ([]byte, error) {
	var r convertRequest
	if err := decodeRequest(req, &r); err != nil {
		return nil, err
	}
	from := upper(r.From)
	to := upper(r.To)
	if from == "" {
		from = o.e.DefaultBase()
	}
	if to == "" {
		to = o.e.DefaultBase()
	}
	amount := 1.0
	if r.Amount != nil {
		amount = *r.Amount
	}

	now := o.now().UTC()
	c, err := fx.Describe(o.e.Snapshot(), from, to, amount, now)
	if err != nil {
		// The HTTP API distinguishes these with a status code; the ABI has no
		// status codes, so the error string is the whole answer. The sentinel
		// text is fx's own, unwrapped, so a binding can match on it.
		return nil, fmt.Errorf("openrate: convert %s->%s: %w", from, to, err)
	}
	return marshal(map[string]any{
		"from":   c.From,
		"to":     c.To,
		"amount": c.Amount,
		"result": c.Result,
		"rate":   viewOf(c, now),
	})
}

// --- rates -----------------------------------------------------------------

type ratesRequest struct {
	Base string `json:"base"`
}

func (o *engineObj) rates(req []byte) ([]byte, error) {
	var r ratesRequest
	if err := decodeRequest(req, &r); err != nil {
		return nil, err
	}
	base := upper(r.Base)
	if base == "" {
		base = o.e.DefaultBase()
	}

	// One snapshot for the whole response, as serve does: a refresh landing
	// mid-loop would otherwise mix two books under one built_at.
	snap := o.e.Snapshot()
	now := o.now().UTC()

	// The one deliberate difference from GET /api/v1/rates, and it is a
	// difference in the ERROR path only: the HTTP handler answers 200 with an
	// empty book for a base the snapshot does not know, while [openrate.Engine.Rates]
	// returns ErrUnknownBase. The ABI follows the library, because a caller who
	// asked for rates against "ZZZ" and got `{}` has been told nothing. A
	// snapshot with no currencies at all is still "nothing yet" rather than a
	// bad request, exactly as the library has it.
	if len(snap.Currencies) > 0 && !snap.Has(base) {
		return nil, fmt.Errorf("openrate: rates base %s: %w", base, openrate.ErrUnknownBase)
	}

	rates := map[string]rateView{}
	for ccy := range snap.Rebase(base) {
		c, err := fx.Describe(snap, base, ccy, 1, now)
		if err != nil {
			continue
		}
		rates[ccy] = viewOf(c, now)
	}
	return marshal(map[string]any{
		"base":     base,
		"built_at": snap.BuiltAt,
		"rates":    rates,
	})
}

// --- meta ------------------------------------------------------------------

func (o *engineObj) meta(req []byte) ([]byte, error) {
	var ignored struct{}
	if err := decodeRequest(req, &ignored); err != nil {
		return nil, err
	}
	snap := o.e.Snapshot()
	return marshal(map[string]any{
		"default_base": o.e.DefaultBase(),
		"built_at":     snap.BuiltAt,
		"currencies":   snap.Currencies,
		"sources":      o.statuses(),
	})
}

// --- load ------------------------------------------------------------------

// loadRequest is the zero-network path across the boundary: a set of quoted
// edges, in fx.Edge's own JSON form, materialized into a snapshot and installed
// on the engine.
//
// It has no HTTP counterpart — the server is read-only — and it is the reason a
// host in another language can use openrate as a pure calculator over rates it
// obtained some other way (a file, a cache, its own feed) without openrate ever
// touching a socket.
type loadRequest struct {
	Edges   []fx.Edge  `json:"edges"`
	BuiltAt *time.Time `json:"built_at"`
}

func (o *engineObj) load(req []byte) ([]byte, error) {
	var r loadRequest
	if err := decodeRequest(req, &r); err != nil {
		return nil, err
	}
	built := o.now().UTC()
	if r.BuiltAt != nil {
		built = r.BuiltAt.UTC()
	}

	// Group by source so the graph keeps its per-source structure: that is what
	// makes a later refresh able to replace one source without disturbing the
	// rest, and it is how the "sources" list on every rate gets populated.
	g := fx.NewGraph()
	bySource := map[string][]fx.Edge{}
	order := []string{}
	for _, e := range r.Edges {
		if _, seen := bySource[e.Source]; !seen {
			order = append(order, e.Source)
		}
		if e.Time.IsZero() {
			e.Time = built
		}
		bySource[e.Source] = append(bySource[e.Source], e)
	}
	for _, name := range order {
		g.Replace(name, bySource[name])
	}

	snap := g.Materialize(built)
	o.e.Load(snap)
	return marshal(map[string]any{
		"built_at":   snap.BuiltAt,
		"currencies": snap.Currencies,
	})
}

// upper is serve's own currency normalization, character for character, so
// "  usd " names the same currency over the ABI as it does over HTTP.
func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
