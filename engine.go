package openrate

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/vul-os/openrate/fx"
)

// Conversion is a rate with everything behind it: the arithmetic, the
// provenance and the grade. See [fx.Conversion].
type Conversion = fx.Conversion

// The errors Convert returns, re-exported so a caller need not import fx to
// test them with errors.Is.
var (
	ErrUnknownPair      = fx.ErrUnknownPair
	ErrInvalidAmount    = fx.ErrInvalidAmount
	ErrAmountOutOfRange = fx.ErrAmountOutOfRange
	ErrUnknownBase      = fx.ErrUnknownBase
)

// EngineOptions configures an Engine. The zero value is valid: base ZAR, the
// default logger, the real clock.
type EngineOptions struct {
	// Base is the default presentation base currency (default "ZAR"). It is
	// only a default — every method takes the currencies it operates on.
	Base string
	// Logger receives the engine's (very sparse) diagnostics. nil means
	// slog.Default(); pass slog.New(slog.DiscardHandler) for silence.
	Logger *slog.Logger
	// Now is the clock used for ageing and grading rates. nil means time.Now.
	// A test can pin it and get byte-identical answers on every run.
	Now func() time.Time
}

// Engine answers questions about whatever snapshot it currently holds.
//
// Constructing one starts no goroutines, opens no sockets, reads no environment
// and sends no packets — now or ever. An Engine on its own is inert: it answers
// from the snapshot it was given, and until something gives it one, it honestly
// answers "I don't know". That is the point. A host can construct an Engine
// behind a feature flag that is off and be certain the process is unchanged.
//
// To put rates in it, either call Load with a snapshot you built or obtained
// yourself, or construct a [Refresher] over it — the one type in openrate that
// touches the network.
//
// All methods are safe for concurrent use.
type Engine struct {
	base string
	log  *slog.Logger
	now  func() time.Time

	mu   sync.RWMutex
	snap *fx.Snapshot

	// ready is closed the first time a non-empty snapshot is loaded. A
	// consumer waits on it through Refresher.Ready.
	readyOnce sync.Once
	ready     chan struct{}
}

// NewEngine returns an Engine holding an empty snapshot.
func NewEngine(opts EngineOptions) *Engine {
	base := normalize(opts.Base)
	if base == "" {
		base = "ZAR"
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Engine{
		base: base,
		log:  log,
		now:  now,
		// An empty snapshot rather than nil, so every read path works before
		// the first refresh instead of having to special-case a nil.
		snap:  fx.NewGraph().Materialize(now().UTC()),
		ready: make(chan struct{}),
	}
}

// DefaultBase is the presentation base the engine was configured with.
func (e *Engine) DefaultBase() string { return e.base }

// Snapshot returns the immutable all-pairs view the engine is currently
// answering from. It is never nil. Treat it as read-only: it is shared with
// every other reader.
func (e *Engine) Snapshot() *fx.Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snap
}

// Load installs a snapshot, replacing whatever the engine held. This is the
// zero-network path: build a snapshot from an fx.Graph you filled yourself,
// from a cache, from a file, from another process — the engine does not care
// where it came from.
//
// Loading a snapshot that has at least one currency is what makes the engine
// ready (see Refresher.Ready). A nil snapshot is ignored.
func (e *Engine) Load(snap *fx.Snapshot) {
	if snap == nil {
		return
	}
	e.mu.Lock()
	e.snap = snap
	e.mu.Unlock()

	if len(snap.Currencies) > 0 {
		e.readyOnce.Do(func() { close(e.ready) })
	}
	e.log.Debug("openrate: snapshot loaded",
		slog.Int("currencies", len(snap.Currencies)),
		slog.Time("built_at", snap.BuiltAt))
}

// Convert answers "what is amount of from, in to", with the full provenance of
// the number attached.
//
// It returns ErrUnknownPair if the two currencies are not connected in the
// current snapshot (which is what an engine that has never been loaded says
// about everything), ErrInvalidAmount for a non-finite amount, and
// ErrAmountOutOfRange when amount × rate has no representable value.
func (e *Engine) Convert(from, to string, amount float64) (Conversion, error) {
	return fx.Describe(e.Snapshot(), normalize(from), normalize(to), amount, e.now().UTC())
}

// Rates returns every currency expressed against base: result[X] reads as
// "1 base = result[X].Rate units of X".
//
// It returns ErrUnknownBase if the snapshot knows some currencies but not that
// one. An engine that holds no rates at all returns an empty map and no error —
// "nothing yet" is a readiness question, not a bad request.
func (e *Engine) Rates(base string) (map[string]fx.Pair, error) {
	snap := e.Snapshot()
	base = normalize(base)
	if base == "" {
		base = e.base
	}
	if len(snap.Currencies) == 0 {
		return map[string]fx.Pair{}, nil
	}
	if !snap.Has(base) {
		return nil, ErrUnknownBase
	}
	return snap.Rebase(base), nil
}

// normalize is how every currency code entering the public API is read: upper
// case, no surrounding space. "  usd " and "USD" are the same currency.
func normalize(ccy string) string { return strings.ToUpper(strings.TrimSpace(ccy)) }
