// Package abi is the language-neutral half of openrate's C ABI: a handle
// registry, a method dispatcher, and the JSON shapes that cross the boundary.
//
// It contains no cgo. That is deliberate — everything with behaviour lives
// here, where it is testable by a normal `go test` on a machine with no C
// toolchain, and the cgo layer above it (package main in ../) is a thin
// translation of C strings to Go strings and back. A bug that matters is
// therefore reachable by an ordinary Go test, and the part that can only be
// exercised through a real shared library stays small enough to read.
//
// This is a separate module (see ../go.mod), so the repository root's
// `go test ./...` does NOT run these tests. CI runs them explicitly through
// scripts/go-test-gate.sh, with a floor and a list of required test names, for
// the same reason it does that for embedtest: a suite nobody notices has
// stopped running is worse than no suite.
//
// # The design this must not break
//
// openrate's central property is that an Engine computes and a Refresher
// fetches, and that the two are constructed separately, so a host that builds
// only an Engine sends no packets — ever, not "unless configured". The ABI
// preserves that shape rather than papering over it: openrate_new builds an
// Engine and nothing else, and a Refresher is a second, explicit construction
// with its own handle and its own lifetime. There is no "start everything"
// entry point, because there is no such thing in the library.
//
// # Handles
//
// Handles are uint64 keys into a registry guarded by a mutex, never pointers
// cast to integers. A host that uses a handle after closing it, or invents one,
// gets an error string back rather than a segfault in someone else's address
// space.
package abi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/vul-os/openrate"
)

// object is one live thing behind a handle. Both kinds answer openrate_call,
// which is what keeps the C surface to a single dispatch function.
type object interface {
	// kindName is used in error messages, so "convert on a refresher" says
	// which kind of handle the caller actually passed.
	kindName() string
	call(method string, req []byte) ([]byte, error)
	// close releases whatever the object started. It must be idempotent-safe
	// from the registry's point of view: the registry only calls it once.
	close()
}

// LOCK ORDER, for the whole package:
//
//	engineObj.mu  ->  refresherObj.mu  ->  the registry mu below
//
// The registry lock is the INNERMOST one. Nothing in this file calls into an
// object while holding mu — register/install/lookup/take/OpenHandles only touch
// the map — so the registry can never be the outer lock in a cycle, and an
// object is free to publish or retire a handle while holding its own lock. That
// is what makes engineObj.adopt able to register a refresher and append it to
// the engine's child list in ONE critical section (see engine.go), which is the
// property that leaves no window for a concurrent engine close to slip through.
//
// The corollary, and the rule to keep: engineObj.close and refresherObj.close
// release their own lock BEFORE calling take() on a child or detaching from a
// parent, so the outer-to-inner direction is never inverted.
var (
	mu      sync.Mutex
	next    uint64 = 1
	objects        = map[uint64]object{}
)

// reserve allocates a handle without publishing anything under it. Handles
// start at 1 so that 0 is always invalid, which is what openrate_new returns on
// failure.
//
// It is separate from install because every object here needs to know its own
// handle before it becomes reachable: a refresher so the engine that owns it can
// unregister it, an engine so it can name itself in the "handle is not open"
// error it returns when a refresher is built over it as it closes. Writing the
// handle in afterwards would mean writing it while another thread could already
// be closing the object.
func reserve() uint64 {
	mu.Lock()
	defer mu.Unlock()
	h := next
	next++
	return h
}

func install(h uint64, obj object) {
	mu.Lock()
	defer mu.Unlock()
	objects[h] = obj
}

// errHandleNotOpen is THE error a host sees for a handle that is not live, and
// it is one function so that every way of discovering that fact produces the
// same sentence. lookup returns it for a handle that is gone from the registry;
// an object returns it for itself when a close landed between the caller's
// lookup and the caller's method call. Those two are the same event from the
// host's side — the handle was closed — and a host that matches on "not open"
// must not have to also match on some second phrasing that only appears when it
// loses a race. include/openrate.h promises exactly this one outcome.
func errHandleNotOpen(h uint64) error {
	return fmt.Errorf("openrate: handle %d is not open (never created, or already closed)", h)
}

func lookup(h uint64) (object, error) {
	mu.Lock()
	defer mu.Unlock()
	obj, ok := objects[h]
	if !ok {
		return nil, errHandleNotOpen(h)
	}
	return obj, nil
}

// take removes a handle and returns what was behind it, or ok=false if the
// handle was not open. Removal happens under the lock so two concurrent closes
// cannot both run the teardown.
func take(h uint64) (object, bool) {
	mu.Lock()
	defer mu.Unlock()
	obj, ok := objects[h]
	if ok {
		delete(objects, h)
	}
	return obj, ok
}

// OpenHandles reports how many handles are currently live. It exists for tests
// — a leak here is a leak in the host's address space that nothing else would
// notice.
func OpenHandles() int {
	mu.Lock()
	defer mu.Unlock()
	return len(objects)
}

// New builds an Engine and returns its handle.
//
// configJSON may be empty or "null". Fields:
//
//	{"base": "ZAR", "quiet": false}
//
// base is the presentation default (see [openrate.EngineOptions]); quiet
// silences the library's slog output, which a host embedding this in another
// language's process usually wants, since it lands on that process's stderr.
//
// This constructs an Engine. It starts no goroutine, opens no socket, reads no
// environment variable and sends no packet.
func New(configJSON string) (uint64, error) {
	var cfg engineConfig
	if err := decodeConfig(configJSON, &cfg); err != nil {
		return 0, err
	}
	e := openrate.NewEngine(openrate.EngineOptions{
		Base:   cfg.Base,
		Logger: logger(cfg.Quiet),
	})
	h := reserve()
	install(h, &engineObj{e: e, handle: h, now: time.Now})
	return h, nil
}

// NewRefresher builds a Refresher over the Engine behind engineHandle and
// returns its own, separate handle.
//
// configJSON fields:
//
//	{"sources": "ecb,coinbase", "interval_ms": 3600000,
//	 "fetch_timeout_ms": 50000, "quiet": false}
//
// sources is a comma-separated spec resolved by [fxsource.Build], which is
// pure: it never consults the environment, so no API key a host happens to
// carry can widen the set. An empty spec means openrate's compiled-in defaults.
//
// Constructing a Refresher still sends no packet. Fetching begins when the
// caller invokes the "refresh" or "start" method on this handle.
func NewRefresher(engineHandle uint64, configJSON string) (uint64, error) {
	obj, err := lookup(engineHandle)
	if err != nil {
		return 0, err
	}
	eo, ok := obj.(*engineObj)
	if !ok {
		return 0, fmt.Errorf("openrate: handle %d is a %s, not an engine; a refresher is built over an engine",
			engineHandle, obj.kindName())
	}
	h := reserve()
	ro, err := newRefresherObj(eo, h, configJSON)
	if err != nil {
		return 0, err
	}
	// One call, not two. Publishing the handle and adopting it onto the engine
	// used to be separate steps, and a concurrent openrate_close(engine) landing
	// between them left a refresher that was live in the registry but absent
	// from the child list the engine had already snapshotted — a handle nothing
	// could ever close, so openrate_open_handles() never returned to zero. adopt
	// does both under the engine's lock, and refuses if the engine is already
	// closing, so the refresher is either fully owned or never born.
	if err := eo.adopt(ro); err != nil {
		return 0, err
	}
	return h, nil
}

// Call dispatches one unary method against a handle and returns the response
// JSON. It returns an error for an unknown handle, a method the handle's kind
// does not have, malformed request JSON, or a failure from the library itself
// (an unknown currency pair, say). There is no partial success: on error the
// response is empty and the error string is the whole story.
func Call(h uint64, method, requestJSON string) (string, error) {
	obj, err := lookup(h)
	if err != nil {
		return "", err
	}
	out, err := obj.call(method, []byte(requestJSON))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// Close releases a handle. Closing an engine also stops and releases every
// refresher built over it, so a host cannot leak a running ticker by closing
// things in the wrong order. Closing an unknown or already-closed handle is a
// no-op, not an error — a double free from the host must not be fatal.
func Close(h uint64) {
	obj, ok := take(h)
	if !ok {
		return
	}
	obj.close()
}

// decodeConfig accepts the empty string and "null" as "no configuration", so a
// C caller may pass NULL for a config it has nothing to say about.
func decodeConfig(s string, into any) error {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(trimmed), into); err != nil {
		return fmt.Errorf("openrate: config is not valid JSON: %w", err)
	}
	return nil
}

// decodeRequest is decodeConfig for a call's request document. It is a separate
// function only so the error message names the right thing.
func decodeRequest(b []byte, into any) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(trimmed), into); err != nil {
		return fmt.Errorf("openrate: request is not valid JSON: %w", err)
	}
	return nil
}

// logger returns the library default, or a discarding one when the host asked
// for quiet. The library's own default writes to slog.Default(), which in a
// hosted process is that process's stderr — worth being able to switch off, and
// worth NOT switching off by default, so a refresh failure is still visible.
func logger(quiet bool) *slog.Logger {
	if quiet {
		return slog.New(slog.DiscardHandler)
	}
	return nil
}

// marshal is the one place a response becomes bytes. Compact, not indented: the
// HTTP API indents for a human reading curl output, and an FFI response is read
// by a parser.
func marshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("openrate: encoding the response failed: %w", err)
	}
	return b, nil
}
