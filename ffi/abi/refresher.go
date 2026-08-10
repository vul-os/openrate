package abi

import (
	"context"
	"fmt"
	"math"
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
	// parent is the engine this refresher was built over, so closing the
	// refresher can take itself out of that engine's child list. Set at
	// construction and never reassigned, so it needs no lock.
	parent *engineObj

	mu sync.Mutex
	// closed is TERMINAL and is the whole of fix A. It is deliberately not the
	// same thing as "not running": "stop" is a host-callable, REVERSIBLE
	// operation — start-after-stop is documented as legal, and tested — while
	// close retires the handle for good. One flag covering both would have made
	// "stop" poison the refresher; no flag at all let a start that had already
	// looked its handle up install a loop on an object Close had just removed
	// from the registry, leaving a ticker nothing could ever reach again.
	closed  bool
	cancel  context.CancelFunc
	stopped chan struct{}

	// inflight is every BLOCKING call currently running on this object, keyed
	// by a serial number, valued by the cancellation for that call's context.
	// It is what makes close wait, and it is a refcount rather than a flag
	// because a host may have several threads on one handle.
	//
	// A `closed` check at the top of refresh would not have been a fix. It is
	// pure check-then-act: the check passes, close runs to completion, and the
	// fetch then starts against a handle that is gone — one request, no loop,
	// but still a use-after-close, and still a packet sent after
	// openrate_close() returned. The interlock has to be "close cannot finish
	// while a call is running", which needs a count and something to wait on.
	inflight map[uint64]context.CancelFunc
	nextCall uint64
	// drained is created by close when it finds calls in flight, and closed by
	// the last one to leave. Nil the rest of the time, so the common close —
	// nothing running — allocates nothing and waits on nothing.
	drained chan struct{}
}

// enter registers a blocking call and refuses if the handle is already closed.
// The returned id is what leave takes back.
//
// cancel is stored so close can interrupt the call rather than merely outlast
// it. Without that, closing a handle while a refresh is fetching would block
// openrate_close() for as long as the slowest source takes — up to the 50s
// per-source default — which is not a thing a host unloading a library can
// afford. Cancelling is also the honest answer to the caller: the handle it
// asked on no longer exists.
func (o *refresherObj) enter(cancel context.CancelFunc) (uint64, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return 0, errHandleNotOpen(o.handle)
	}
	o.nextCall++
	if o.inflight == nil {
		o.inflight = map[uint64]context.CancelFunc{}
	}
	o.inflight[o.nextCall] = cancel
	return o.nextCall, nil
}

// leave retires a call, and releases close if it was the last one it was
// waiting for.
//
// The channel is taken out of the struct in the SAME critical section that
// decides to close it, so it is closed exactly once however many callers are
// leaving at the same moment.
func (o *refresherObj) leave(id uint64) {
	o.mu.Lock()
	delete(o.inflight, id)
	var release chan struct{}
	if o.closed && len(o.inflight) == 0 && o.drained != nil {
		release, o.drained = o.drained, nil
	}
	o.mu.Unlock()
	if release != nil {
		close(release)
	}
}

// newRefresherObj parses the config and constructs the Refresher. It performs
// no I/O — constructing a Refresher never has.
func newRefresherObj(parent *engineObj, handle uint64, configJSON string) (*refresherObj, error) {
	var cfg refresherConfig
	if err := decodeConfig(configJSON, &cfg); err != nil {
		return nil, err
	}
	interval, err := configDuration("interval_ms", cfg.IntervalMS)
	if err != nil {
		return nil, err
	}
	fetchTimeout, err := configDuration("fetch_timeout_ms", cfg.FetchTimeoutMS)
	if err != nil {
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
		r: openrate.NewRefresher(parent.e, openrate.RefreshOptions{
			Sources:      srcs,
			Interval:     interval,
			FetchTimeout: fetchTimeout,
			Logger:       logger(cfg.Quiet),
		}),
		handle: handle,
		parent: parent,
	}, nil
}

// maxDurationMS is the largest millisecond count a [time.Duration] can hold. A
// Duration is an int64 of NANOSECONDS, so `time.Duration(ms) * time.Millisecond`
// overflows above this — and NOT always into something obviously wrong. The
// interesting values wrap back into a small POSITIVE duration: interval_ms
// 288230376151711745 multiplies out to exactly 1ms, so a host asking to refresh
// once every nine million years would instead have hammered every configured
// source a thousand times a second, from a loop it believed to be dormant.
// interval_ms is also the one config field with no upper bound a reader would
// think to check, because "bigger" reads as "safer".
const maxDurationMS = int64(math.MaxInt64) / int64(time.Millisecond) // 9223372036854 ms, ~292 years

// configDuration converts one millisecond field of a config document, refusing
// anything the conversion cannot represent.
//
// Refusing, not clamping: this is a CONSTRUCTOR argument, the ABI already
// rejects a config it cannot honour (see the source-spec case above), and
// silently substituting a cadence the host did not ask for is how the overflow
// went unnoticed in the first place. The bound is symmetric because a negative
// out-of-range value wraps just as freely as a positive one; a negative value
// that does fit keeps its existing meaning, which is "use openrate's default".
func configDuration(field string, ms int64) (time.Duration, error) {
	if ms > maxDurationMS || ms < -maxDurationMS {
		return 0, fmt.Errorf("openrate: %s is %d, which does not fit in a duration; the largest "+
			"value is %d (about 292 years). Leave it 0 for openrate's default.", field, ms, maxDurationMS)
	}
	return time.Duration(ms) * time.Millisecond, nil
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

// stopLoop cancels the background loop, if one is running, and waits for it to
// return. Waiting matters: a host that closes a handle and then unloads the
// library must not have a goroutine still writing into the engine.
//
// It is REVERSIBLE. This is the "stop" method's whole implementation, and
// "start" after it is legal — see start's doc comment. It deliberately does not
// touch o.closed.
func (o *refresherObj) stopLoop() {
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

// close is TERMINAL: the registry has already retired this handle and nothing
// may start a loop on this object, or begin a call against it, again. It is the
// other half of the interlock with start AND with every blocking method.
//
// Marking closed and reading the cancel happen in ONE critical section, which
// is what makes the race unwinnable in either direction. Either start got the
// lock first, in which case close sees the cancel it installed and stops the
// loop; or close got there first, in which case start sees closed and refuses
// with "handle is not open". What used to happen was neither: start read the
// object from the registry, close removed it and found no cancel to cancel,
// and start then attached a goroutine to an object no handle pointed at — so
// "stop" was unreachable, openrate_close() was a permanent no-op, and the loop
// fetched forever while openrate_open_handles() reported zero.
//
// The same critical section takes the in-flight calls. A "refresh" that had
// already resolved its handle used to run to completion after close returned —
// one fetch, no loop, so far less severe than the escaped ticker above, but the
// same shape: a packet sent by a handle that no longer exists, and a write into
// an engine the host may already have closed. close now cancels those calls and
// then WAITS for them, so when it returns the promise in include/openrate.h —
// nothing of this refresher is still running — is true of the one-shot path too
// and not only of the loop.
//
// Nothing here touches "stop", which stays a reversible pause: start-after-stop
// and refresh-after-stop are both part of the contract.
func (o *refresherObj) close() {
	o.mu.Lock()
	o.closed = true
	cancel, stopped := o.cancel, o.stopped
	o.cancel, o.stopped = nil, nil
	var pending []context.CancelFunc
	var drained chan struct{}
	if len(o.inflight) > 0 {
		drained = make(chan struct{})
		o.drained = drained
		for _, c := range o.inflight {
			pending = append(pending, c)
		}
	}
	o.mu.Unlock()
	if cancel != nil {
		cancel()
		<-stopped
	}
	for _, c := range pending {
		c()
	}
	if drained != nil {
		<-drained
	}
	// After the lock is dropped: detach takes the engine's lock, which is the
	// OUTER lock of the two. Holding o.mu across it would invert the order.
	o.parent.detach(o)
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
	// Clamped rather than refused, which is the opposite of what configDuration
	// does with the same overflow, and for a reason: a deadline of 292 years is
	// indistinguishable from the one the caller asked for, so there is nothing
	// to tell them. Left unclamped it is the reverse of harmless — a caller
	// asking for an absurdly generous timeout would get 1ms and read a spurious
	// "context deadline exceeded" as a failure of the fetch.
	ms := t.TimeoutMS
	if ms > maxDurationMS {
		ms = maxDurationMS
	}
	return context.WithTimeout(context.Background(), time.Duration(ms)*time.Millisecond)
}

// refresh is the one-shot fetch: it blocks until every source has answered or
// failed, then returns the resulting per-source status. This is the call that
// opens sockets, and it is reachable only from a refresher handle that a host
// explicitly created.
//
// enter/leave is what keeps it inside the handle's lifetime — see enter. A
// close that lands after this call has resolved its handle refuses nothing
// retroactively; it cancels ctx and waits here.
func (o *refresherObj) refresh(req []byte) ([]byte, error) {
	var r timeoutRequest
	if err := decodeRequest(req, &r); err != nil {
		return nil, err
	}
	ctx, cancel := r.context()
	defer cancel()
	id, err := o.enter(cancel)
	if err != nil {
		return nil, err
	}
	defer o.leave(id)
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
//
// Start after STOP is legal, and stays legal — a host that pauses fetching and
// resumes it later is doing something reasonable. Start after CLOSE is not, and
// fails with the same "handle is not open" a call on a retired handle gets: by
// the time close has run, the number the host would have to pass to stop this
// loop again no longer refers to anything.
func (o *refresherObj) start(req []byte) ([]byte, error) {
	var ignored struct{}
	if err := decodeRequest(req, &ignored); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, errHandleNotOpen(o.handle)
	}
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
	o.stopLoop()
	return marshal(map[string]any{"running": false})
}

// ready blocks until the engine holds a snapshot with at least one currency, or
// the timeout expires. It does not itself fetch — something must be refreshing,
// or it simply waits, which is exactly [openrate.Refresher.Ready]'s contract.
//
// It takes the same refcount as refresh. It sends no packets, so it is not the
// use-after-close that mattered, but it is the other call that can still be
// parked inside this library when a host decides to unload it — and a goroutine
// in a shared object that has been dlclose'd is a segfault, not an error. close
// cancels it, so it returns "context canceled" rather than blocking out its
// timeout against a handle that no longer exists.
func (o *refresherObj) ready(req []byte) ([]byte, error) {
	var r timeoutRequest
	if err := decodeRequest(req, &r); err != nil {
		return nil, err
	}
	ctx, cancel := r.context()
	defer cancel()
	id, err := o.enter(cancel)
	if err != nil {
		return nil, err
	}
	defer o.leave(id)
	if err := o.r.Ready(ctx); err != nil {
		return nil, fmt.Errorf("openrate: ready: %w", err)
	}
	return marshal(map[string]any{"ready": true})
}
