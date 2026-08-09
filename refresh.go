package openrate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/fxsource"
	"github.com/vul-os/openrate/internal/redact"
)

// SourceStatus is the outcome of one source's last fetch. See [fxsource.Status].
type SourceStatus = fxsource.Status

// ErrNoSources is returned by Refresh when the Refresher was built with an
// empty source set: there is nothing to fetch, and silently succeeding would
// leave a caller waiting forever for rates that can never arrive.
var ErrNoSources = errors.New("openrate: no sources configured")

// DefaultFetchTimeout bounds a single source's fetch. It is generous because
// the SARB host can take tens of seconds just to connect.
const DefaultFetchTimeout = 50 * time.Second

// DefaultRefreshInterval is the cadence Run uses when none is given.
const DefaultRefreshInterval = time.Hour

// RefreshOptions configures a Refresher. Sources is the only field with no
// useful default: a Refresher exists to fetch, and what it fetches has to be
// the caller's decision, never an environment variable's.
type RefreshOptions struct {
	// Sources are the providers to fetch from, explicitly. Use
	// fxsource.Build("ecb,coinbase") for a named set, or fxsource.FromEnv() to
	// opt in to environment configuration.
	Sources []fxsource.Source
	// Interval is how often Run refreshes (default 1h). Ignored by Refresh.
	Interval time.Duration
	// FetchTimeout bounds each individual source fetch (default 50s).
	FetchTimeout time.Duration
	// Logger receives one warning per failed source fetch. nil means
	// slog.Default(); pass slog.New(slog.DiscardHandler) for silence.
	Logger *slog.Logger
}

// Refresher fetches from sources and feeds the results into an Engine. It is
// the only part of openrate that opens a socket.
//
// Constructing one still starts nothing: no goroutine, no connection, no
// packet. Fetching happens when the caller calls Refresh, or runs Run. That
// separation is the whole design — a host that never builds a Refresher cannot
// acquire an outbound dependency by accident, and one that builds it can decide
// exactly when the process talks to the internet.
//
// A Refresher owns its own edge graph, so each source's contribution can be
// replaced atomically on refresh while the Engine keeps serving the previous
// snapshot. All methods are safe for concurrent use.
type Refresher struct {
	engine  *Engine
	srcs    []fxsource.Source
	log     *slog.Logger
	timeout time.Duration
	every   time.Duration

	mu     sync.Mutex // guards g and status; never held across network I/O
	g      *fx.Graph
	status map[string]*fxsource.Status
	order  []string // source names in configuration order, for stable Status()
}

// NewRefresher returns a Refresher that will feed e. It performs no I/O.
func NewRefresher(e *Engine, opts RefreshOptions) *Refresher {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	timeout := opts.FetchTimeout
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}
	every := opts.Interval
	if every <= 0 {
		every = DefaultRefreshInterval
	}
	r := &Refresher{
		engine:  e,
		srcs:    append([]fxsource.Source(nil), opts.Sources...),
		log:     log,
		timeout: timeout,
		every:   every,
		g:       fx.NewGraph(),
		status:  map[string]*fxsource.Status{},
	}
	for _, s := range r.srcs {
		name := s.Name()
		if _, dup := r.status[name]; dup {
			continue
		}
		r.status[name] = &fxsource.Status{Name: name}
		r.order = append(r.order, name)
	}
	return r
}

// Status returns a copy of the per-source fetch outcomes, in the order the
// sources were configured.
func (r *Refresher) Status() []SourceStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SourceStatus, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, *r.status[name])
	}
	return out
}

// Refresh fetches every source once, concurrently, and loads the result into
// the Engine. It is synchronous: when it returns, the Engine holds everything
// this refresh could get. Call it on your own schedule — a cron, a request, a
// startup hook — and never run Run at all if that suits you better.
//
// A source that fails keeps its previous edges and records the error in Status;
// the rest of the snapshot is unaffected. Refresh returns an error only when
// nothing at all could be fetched: every source failed, or there are none.
// That distinction matters for a caller doing a one-shot fetch at boot, which
// wants to know "do I have rates?" and not "was everything perfect?".
func (r *Refresher) Refresh(ctx context.Context) error {
	if len(r.srcs) == 0 {
		return ErrNoSources
	}

	type result struct {
		name  string
		edges []fx.Edge
		err   error
	}
	results := make(chan result, len(r.srcs))
	for _, src := range r.srcs {
		go func(src fxsource.Source) {
			c, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()
			edges, err := src.Fetch(c)
			results <- result{src.Name(), edges, err}
		}(src)
	}

	// Apply each result as it arrives, re-materializing progressively. Fast
	// sources (Coinbase ~1s) populate the snapshot immediately rather than
	// waiting for the slowest (SARB). The lock is held only for the fast
	// apply+materialize, never during network I/O, so reads are never blocked
	// on a slow source.
	var failures []error
	ok := 0
	for range r.srcs {
		res := <-results
		r.mu.Lock()
		now := time.Now().UTC()
		st := r.status[res.name]
		if res.err != nil {
			// Paid sources carry their key in the request URL, which net/http
			// echoes back inside *url.Error; redact before logging or exposing.
			safe := redact.Error(res.err)
			if st != nil {
				st.LastError = safe.Error()
			}
			failures = append(failures, fmt.Errorf("%s: %w", res.name, safe))
			r.log.Warn("openrate: source fetch failed",
				slog.String("source", res.name), slog.String("error", safe.Error()))
		} else {
			ok++
			if st != nil {
				st.LastError = ""
				st.LastOK = now
				st.Edges = len(res.edges)
			}
			r.g.Replace(res.name, res.edges)
		}
		snap := r.g.Materialize(now)
		r.mu.Unlock()
		r.engine.Load(snap)
	}

	if ok == 0 {
		return fmt.Errorf("openrate: every source failed: %w", errors.Join(failures...))
	}
	return nil
}

// Run refreshes immediately, then on the configured interval until ctx is
// cancelled. It is a blocking loop — the caller decides whether it gets a
// goroutine (`go r.Run(ctx)`) and owns the cancellation.
//
// It returns ctx.Err() on cancellation, and ErrNoSources immediately if there
// is nothing to fetch. Individual refresh failures do not stop the loop: a
// source being down is a transient condition, and the next tick retries.
func (r *Refresher) Run(ctx context.Context) error {
	if len(r.srcs) == 0 {
		return ErrNoSources
	}
	if err := r.Refresh(ctx); err != nil {
		r.log.Warn("openrate: initial refresh produced nothing", slog.String("error", err.Error()))
	}
	t := time.NewTicker(r.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			if err := r.Refresh(ctx); err != nil {
				r.log.Warn("openrate: refresh produced nothing", slog.String("error", err.Error()))
			}
		}
	}
}

// Ready blocks until the Engine holds a snapshot with at least one currency in
// it, or ctx is done — in which case it returns ctx.Err().
//
// This is the signal the HTTP health endpoint never gave: the engine
// materializes an EMPTY snapshot at construction, so "serving" and "has any
// rate" were two different facts and only the first was observable. A caller
// that must not answer with an empty book waits here first.
//
// Ready does not itself fetch. Something has to be refreshing — Run in a
// goroutine, or a Refresh in flight — or it will simply wait.
func (r *Refresher) Ready(ctx context.Context) error {
	select {
	case <-r.engine.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
