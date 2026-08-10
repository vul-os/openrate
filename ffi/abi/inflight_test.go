package abi

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vul-os/openrate/fxsource"
)

// Finding E: "refresh" surviving its own handle.
//
// This is the third lifecycle race in this suite and the quietest. The escaped
// ticker (finding A, in lifecycle_test.go) fetched forever; this one fetches
// exactly once. It is still a use-after-close: openrate_call(h, "refresh")
// resolves the handle and THEN calls the method, so a close landing in between
// left a fetch running against a retired handle, writing into an engine the
// host may already have closed, and putting a packet on the wire AFTER
// openrate_close() returned.
//
// A `closed` check at the top of refresh would not have fixed it. That is pure
// check-then-act — the check passes, close runs to completion, the fetch starts
// anyway — which is why the fix is a refcount close waits on.
//
// As with the rest of this file's neighbours, none of this is a DATA race:
// every access was already under a lock. `-race` sees nothing. What these tests
// measure instead is the observable promise: when openrate_close() returns,
// this library is not going to send anything else.

// blockingTransport answers the ECB file, but only after the test lets it
// through. It is how "a refresh is genuinely in flight right now" becomes a
// state a test can be in rather than a window it has to hit.
type blockingTransport struct {
	requests  atomic.Int64
	entered   chan struct{} // one token per request that has reached the wire
	release   chan struct{} // closed by the test to let them all finish
	releaseAt sync.Once
}

func newBlockingTransport() *blockingTransport {
	return &blockingTransport{
		entered: make(chan struct{}, 16),
		release: make(chan struct{}),
	}
}

// RoundTrip deliberately IGNORES the request context.
//
// That is the case worth testing. Cancelling an in-flight call is close's fast
// path, not its guarantee — a fetch parked in a DNS lookup, a syscall or a
// library that does not take a context finishes when it finishes, and close
// still must not return before it does. A transport that unblocked on
// cancellation would let close through immediately and the wait would never be
// exercised at all.
func (b *blockingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	b.requests.Add(1)
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	if req.URL.String() != fxsource.ECBDailyURL {
		return nil, &countedError{}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(ecbDailyXML)),
		Header:     http.Header{"Content-Type": []string{"application/xml"}},
		Request:    req,
	}, nil
}

// releaseAll lets every parked request finish. It is idempotent so a test can
// call it on its own path and still have it run from a defer.
func (b *blockingTransport) releaseAll() { b.releaseAt.Do(func() { close(b.release) }) }

func blockingNetwork(t *testing.T) *blockingTransport {
	t.Helper()
	bt := newBlockingTransport()
	oldTransport, oldClient := http.DefaultTransport, http.DefaultClient
	http.DefaultTransport = bt
	http.DefaultClient = &http.Client{Transport: bt}
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
		http.DefaultClient = oldClient
		bt.releaseAll() // never leave a fetch parked in a finished test
	})
	return bt
}

// TestCloseWaitsForARefreshAlreadyInFlight is the deterministic half: no race
// window, no iteration count. A refresh is held on the wire, close is called,
// and close must not return until the fetch it cannot cancel away has finished.
func TestCloseWaitsForARefreshAlreadyInFlight(t *testing.T) {
	net := blockingNetwork(t)
	// This defer runs BEFORE any t.Cleanup, so a t.Fatal below cannot leave the
	// engine close waiting on a request nothing will ever release.
	defer net.releaseAll()
	eh := mustNew(t, `{"base":"EUR","quiet":true}`)
	t.Cleanup(func() { Close(eh) })

	rh, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}

	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		_, _ = Call(rh, "refresh", `{}`)
	}()

	// Wait until the fetch is genuinely on the wire. Without this the test
	// would be racing close against a call that had not started, which is a
	// different (already-fixed) window.
	select {
	case <-net.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh never reached the transport; this test never got into the state it needs")
	}

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		Close(rh)
	}()

	// close must be blocked. If it returns here, it returned while a fetch was
	// still running — which is the bug, in its clearest form.
	select {
	case <-closeDone:
		t.Fatal("openrate_close() returned while a refresh was still on the wire. The host is now free " +
			"to unload this library, and there is a goroutine inside it about to write into an engine " +
			"and to finish a request nobody can account for")
	case <-time.After(250 * time.Millisecond):
	}
	select {
	case <-refreshDone:
		t.Fatal("the refresh finished on its own; the transport is not holding it, so the assertion " +
			"above was about nothing")
	default:
	}

	net.releaseAll()

	select {
	case <-refreshDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh never finished after the transport was released")
	}
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatal("openrate_close() never returned after the refresh it was waiting for finished — the " +
			"refcount is not being released")
	}
}

// TestRefreshRacingCloseSendsNothingAfterCloseReturns is finding E as the host
// meets it: two threads, one calling refresh and one calling close, over and
// over.
//
// The measurement is taken INSIDE the racing goroutine, the instant
// openrate_close() returns, and compared with the count once everything has
// settled. That is the only place the bug is visible: race() waits for both
// sides, so by the time the loop body ends the stray fetch has been made and
// counted, and a check written after the join would see nothing wrong.
func TestRefreshRacingCloseSendsNothingAfterCloseReturns(t *testing.T) {
	net := stubNetwork(t)
	before := OpenHandles()

	eh := mustNew(t, `{"base":"EUR","quiet":true}`)

	var served, refused, interrupted, late int64
	for i := 0; i < raceIterations; i++ {
		rh, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
		if err != nil {
			t.Fatalf("NewRefresher on iteration %d: %v", i, err)
		}

		var atCloseReturn int64
		race(i%3,
			func() {
				switch _, err := Call(rh, "refresh", `{}`); {
				case err == nil:
					served++
				case strings.Contains(err.Error(), "not open"):
					refused++
				default:
					// close cancelled a fetch that had already started. Legal,
					// and counted so the totals below add up.
					interrupted++
				}
			},
			func() {
				Close(rh)
				atCloseReturn = net.n.Load()
			},
		)

		if got := net.n.Load(); got != atCloseReturn {
			late++
		}
	}
	Close(eh)

	if late != 0 {
		t.Fatalf("on %d of %d races this library put a request on the wire AFTER openrate_close() had "+
			"returned. The handle was retired, the host is entitled to unload the library, and a fetch "+
			"was still to come — one request rather than a loop, but still a call outliving the handle "+
			"it was made on", late, raceIterations)
	}

	// Both orderings have to have happened, or this is a green tick over an
	// untested window. served+interrupted is "refresh got in first"; refused is
	// "close did".
	if served+interrupted == 0 || refused == 0 {
		t.Errorf("over %d races refresh got in %d time(s) (%d of them cancelled by close) and was "+
			"refused %d — one ordering never occurred, so this test did not exercise the window it "+
			"exists for", raceIterations, served+interrupted, interrupted, refused)
	}
	if got := OpenHandles(); got != before {
		t.Errorf("%d handle(s) leaked over %d refresh/close races", got-before, raceIterations)
	}
}

// TestRefreshOnAClosedRefresherIsRefused pins the other end of the interlock,
// away from any race: once close has run, the object itself refuses, with the
// one sentence include/openrate.h promises for a retired handle.
//
// It reaches past openrate_call deliberately. The registry would refuse the
// handle before the object was ever consulted, so a test that went through Call
// could not tell a working interlock from an absent one.
func TestRefreshOnAClosedRefresherIsRefused(t *testing.T) {
	stubNetwork(t)
	eh := mustNew(t, `{"base":"EUR","quiet":true}`)
	defer Close(eh)

	rh, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}
	obj, err := lookup(rh)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	ro, ok := obj.(*refresherObj)
	if !ok {
		t.Fatalf("handle %d holds a %T", rh, obj)
	}

	// While it is open, both blocking methods work — so the refusals below are
	// the close taking effect and not a broken fixture.
	if _, err := ro.call("refresh", []byte(`{}`)); err != nil {
		t.Fatalf("refresh on an open refresher: %v", err)
	}
	if _, err := ro.call("ready", []byte(`{"timeout_ms":1000}`)); err != nil {
		t.Fatalf("ready on an open refresher: %v", err)
	}

	Close(rh)

	for _, method := range []string{"refresh", "ready"} {
		_, err := ro.call(method, []byte(`{}`))
		if err == nil {
			t.Errorf("%q succeeded on a closed refresher", method)
			continue
		}
		if !strings.Contains(err.Error(), "not open") {
			t.Errorf("%q on a closed refresher said %q; the ABI's one answer for a retired handle is "+
				"\"handle N is not open\", and include/openrate.h promises a host it need match "+
				"nothing else", method, err)
		}
	}
}

// TestStopLeavesRefreshUsable is the guard on the distinction the fix rests on.
// "stop" is a reversible pause and close is terminal, so a refcount added for
// close must not have made stop poison the refresher — the same mistake a
// single shared flag made for "start".
func TestStopLeavesRefreshUsable(t *testing.T) {
	stubNetwork(t)
	eh := mustNew(t, `{"base":"EUR","quiet":true}`)
	defer Close(eh)

	rh, err := NewRefresher(eh, `{"sources":"ecb","interval_ms":50,"quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}
	defer Close(rh)

	for round := 0; round < 3; round++ {
		if _, err := Call(rh, "start", `{}`); err != nil {
			t.Fatalf("start on round %d: %v", round, err)
		}
		if _, err := Call(rh, "stop", `{}`); err != nil {
			t.Fatalf("stop on round %d: %v", round, err)
		}
		if _, err := Call(rh, "refresh", `{"timeout_ms":5000}`); err != nil {
			t.Fatalf("refresh after stop on round %d: %v — stop is a pause, not a retirement", round, err)
		}
		if _, err := Call(rh, "ready", `{"timeout_ms":5000}`); err != nil {
			t.Fatalf("ready after stop on round %d: %v", round, err)
		}
	}
}

// TestSeveralRefreshesInFlightAreAllWaitedFor is the refcount rather than a
// flag: a host with three threads on one handle must have all three finished
// before close returns, not just the first or the last.
func TestSeveralRefreshesInFlightAreAllWaitedFor(t *testing.T) {
	net := blockingNetwork(t)
	// This defer runs BEFORE any t.Cleanup, so a t.Fatal below cannot leave the
	// engine close waiting on a request nothing will ever release.
	defer net.releaseAll()
	eh := mustNew(t, `{"base":"EUR","quiet":true}`)
	t.Cleanup(func() { Close(eh) })

	rh, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}

	const callers = 3
	var running atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			running.Add(1)
			_, _ = Call(rh, "refresh", `{}`)
			running.Add(-1)
		}()
	}
	for i := 0; i < callers; i++ {
		select {
		case <-net.entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d refreshes reached the transport", i, callers)
		}
	}

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		Close(rh)
	}()
	select {
	case <-closeDone:
		t.Fatalf("openrate_close() returned with %d refresh(es) still on the wire", running.Load())
	case <-time.After(250 * time.Millisecond):
	}

	net.releaseAll()
	wg.Wait()
	select {
	case <-closeDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("openrate_close() never returned; %d call(s) still counted as running", running.Load())
	}
	if n := running.Load(); n != 0 {
		t.Errorf("%d refresh(es) were still running when close returned", n)
	}
	if got := net.requests.Load(); got != callers {
		t.Errorf("the transport saw %d request(s), want %d — the fixture did not put the number of "+
			"calls in flight that this test is about", got, callers)
	}
}
