package abi

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fxsource"
)

// refresherConfig is openrate_refresher_new's config document.
type refresherConfig struct {
	// Sources is a comma-separated spec ("ecb,coinbase"). Empty means
	// fxsource.DefaultSources. It is resolved with fxsource.Build, never
	// fxsource.FromEnv: an environment variable the host process happens to
	// carry must not widen what this library fetches, and a paid provider must
	// never be enabled by the mere presence of its key.
	Sources string `json:"sources"`
	// IntervalMS is the "start" loop's cadence. 0 means openrate's default (1h).
	IntervalMS int64 `json:"interval_ms"`
	// FetchTimeoutMS bounds one source's fetch. 0 means openrate's default.
	FetchTimeoutMS int64 `json:"fetch_timeout_ms"`
	// Quiet discards the one warning logged per failed source fetch.
	Quiet bool `json:"quiet"`
}

// refresherObj is one Refresher behind a handle, plus the cancellation for its
// optional background loop.
//
// The loop is opt-in through the "start" method and is the only goroutine this
// ABI ever creates. Nothing here runs until a host asks for it by name.
type refresherObj struct {
	r      *openrate.Refresher
	handle uint64

	mu      sync.Mutex
	cancel  context.CancelFunc
	stopped chan struct{}
}

// newRefresherObj parses the config and constructs the Refresher. It performs
// no I/O — constructing a Refresher never has.
func newRefresherObj(e *openrate.Engine, handle uint64, configJSON string) (*refresherObj, error) {
	var cfg refresherConfig
	if err := decodeConfig(configJSON, &cfg); err != nil {
		return nil, err
	}
	srcs := fxsource.Build(cfg.Sources)
	if len(srcs) == 0 {
		// Build silently drops names it does not know, so an all-typos spec
		// yields an empty set. A Refresher with nothing to fetch would leave
		// "ready" blocking forever, which is a much worse way to learn about a
		// typo than an error here.
		return nil, fmt.Errorf("openrate: source spec %q resolved to no sources; leave it empty for the "+
			"defaults %v, or name adapters fxsource ships", cfg.Sources, fxsource.DefaultSources)
	}
	return &refresherObj{
		r: openrate.NewRefresher(e, openrate.RefreshOptions{
			Sources:      srcs,
			Interval:     time.Duration(cfg.IntervalMS) * time.Millisecond,
			FetchTimeout: time.Duration(cfg.FetchTimeoutMS) * time.Millisecond,
			Logger:       logger(cfg.Quiet),
		}),
		handle: handle,
	}, nil
}

func (o *refresherObj) kindName() string { return "refresher" }

func (o *refresherObj) call(method string, req []byte) ([]byte, error) {
	switch method {
	case "status":
		return o.status(req)
	case "refresh":
		return o.refresh(req)
	case "start":
		return o.start(req)
	case "stop":
		return o.stop(req)
	case "ready":
		return o.ready(req)
	default:
		return nil, fmt.Errorf("openrate: unknown refresher method %q (have: status, refresh, start, stop, ready)", method)
	}
}

// close stops the background loop, if one is running, and waits for it to
// return. Waiting matters: a host that closes a handle and then unloads the
// library must not have a goroutine still writing into the engine.
func (o *refresherObj) close() {
	o.mu.Lock()
	cancel, stopped := o.cancel, o.stopped
	o.cancel, o.stopped = nil, nil
	o.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-stopped
}

// status is the Refresher's per-source fetch outcome, in the same shape and
// under the same key as the "sources" field of GET /api/v1/meta.
func (o *refresherObj) status(req []byte) ([]byte, error) {
	var ignored struct{}
	if err := decodeRequest(req, &ignored); err != nil {
		return nil, err
	}
	st := o.r.Status()
	if st == nil {
		st = []fxsource.Status{}
	}
	return marshal(map[string]any{"sources": st})
}

// timeoutRequest is the shape of every blocking method's request. 0 or absent
// means "no deadline of my own" — the call blocks until the library finishes.
// A host that cannot afford that passes a bound.
type timeoutRequest struct {
	TimeoutMS int64 `json:"timeout_ms"`
}

func (t timeoutRequest) context() (context.Context, context.CancelFunc) {
	if t.TimeoutMS <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), time.Duration(t.TimeoutMS)*time.Millisecond)
}

// refresh is the one-shot fetch: it blocks until every source has answered or
// failed, then returns the resulting per-source status. This is the call that
// opens sockets, and it is reachable only from a refresher handle that a host
// explicitly created.
func (o *refresherObj) refresh(req []byte) ([]byte, error) {
	var r timeoutRequest
	if err := decodeRequest(req, &r); err != nil {
		return nil, err
	}
	ctx, cancel := r.context()
	defer cancel()
	if err := o.r.Refresh(ctx); err != nil {
		return nil, fmt.Errorf("openrate: refresh: %w", err)
	}
	st := o.r.Status()
	if st == nil {
		st = []fxsource.Status{}
	}
	return marshal(map[string]any{"sources": st})
}

// start runs the refresh loop in a goroutine and returns immediately. It is the
// only goroutine this package creates, and "stop" (or closing the handle) ends
// it. Calling start twice is an error rather than a second loop: two tickers
// feeding one engine is never what anybody meant.
func (o *refresherObj) start(req []byte) ([]byte, error) {
	var ignored struct{}
	if err := decodeRequest(req, &ignored); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cancel != nil {
		return nil, fmt.Errorf("openrate: this refresher is already running; stop it before starting it again")
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	o.cancel, o.stopped = cancel, stopped
	go func() {
		defer close(stopped)
		_ = o.r.Run(ctx)
	}()
	return marshal(map[string]any{"running": true})
}

func (o *refresherObj) stop(req []byte) ([]byte, error) {
	var ignored struct{}
	if err := decodeRequest(req, &ignored); err != nil {
		return nil, err
	}
	o.close()
	return marshal(map[string]any{"running": false})
}

// ready blocks until the engine holds a snapshot with at least one currency, or
// the timeout expires. It does not itself fetch — something must be refreshing,
// or it simply waits, which is exactly [openrate.Refresher.Ready]'s contract.
func (o *refresherObj) ready(req []byte) ([]byte, error) {
	var r timeoutRequest
	if err := decodeRequest(req, &r); err != nil {
		return nil, err
	}
	ctx, cancel := r.context()
	defer cancel()
	if err := o.r.Ready(ctx); err != nil {
		return nil, fmt.Errorf("openrate: ready: %w", err)
	}
	return marshal(map[string]any{"ready": true})
}
