package abi

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The lifecycle half of the ABI: what happens when a host closes a handle at
// the same moment as it does something else with it.
//
// These are the first concurrent tests in this suite, and they are deliberately
// NOT data-race tests. Every bug below was a lost update or an ordering
// mistake between two correctly-locked critical sections, so `-race` sees
// nothing wrong with any of them — the detector's job is unsynchronized memory
// access, and there was none. What each test asserts instead is the OBSERVABLE
// invariant the ABI publishes:
//
//   - openrate_open_handles() returns to what it was, and
//   - after close returns, this library makes no further requests.
//
// The second one is why every test here runs against the counting stub
// transport: "no fetches happened" has to be a number, taken after close, and
// compared again after a wait. A loop nobody can reach is invisible to a
// handle count and obvious to a request count.
//
// They run many iterations on purpose. A window measured in a few instructions
// is not hit reliably, so the failure they are meant to catch is a rare event
// that becomes a certainty over thousands of tries — and each test asserts that
// BOTH orderings of its race actually occurred, because a reproducer that only
// ever exercised the safe order would pass while proving nothing.

// Iteration counts, both divisible by 3 so each race mode below gets a third.
//
// They are not the same number because the two windows are not the same size.
// Measured against the unfixed code, the start/close window is hit thousands of
// times per run and 2001 is already overwhelming; the registration window is
// hit between 0.1% and 1.15% of the time, and at 2001 iterations two runs in
// twelve saw no orphan at all. 9999 is what makes it every run.
const (
	raceIterations   = 2001
	orphanIterations = 9999
)

// race runs a and b concurrently and returns when both have finished.
//
// mode 0 releases both from one gate, which is the interesting case and the one
// that finds these bugs. Modes 1 and 2 hand one side a signal sent immediately
// before the other's critical call, so that ordering is all but certain.
//
// The skewed modes exist because the coverage assertions in these tests — "both
// orderings actually occurred" — were themselves flaky without them: on this
// machine an unskewed gate gave all 2001 races to the same goroutine on roughly
// one run in three, and a reproducer that silently only ever exercises the safe
// ordering is the failure mode this whole file is written against.
func race(mode int, a, b func()) {
	gate := make(chan struct{})
	handoff := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-gate
		switch mode {
		case 1:
			close(handoff)
		case 2:
			<-handoff
		}
		a()
	}()
	go func() {
		defer wg.Done()
		<-gate
		switch mode {
		case 1:
			<-handoff
		case 2:
			close(handoff)
		}
		b()
	}()
	close(gate)
	wg.Wait()
}

// TestStartRacingCloseCannotLeaveAnUnstoppableLoop is finding A.
//
// openrate_call(h, "start") resolves the handle and THEN calls the method, and
// openrate_close(h) retires the handle and THEN tears the object down. Between
// those two steps a start could install a cancel function and a goroutine on an
// object that was no longer in the registry: "stop" could not reach it, because
// the handle was gone; close had already run and found nothing to cancel. The
// loop then fetched forever, from every configured source, while
// openrate_open_handles() reported zero — the ABI's own leak detector clean, the
// process still on the network.
func TestStartRacingCloseCannotLeaveAnUnstoppableLoop(t *testing.T) {
	net := stubNetwork(t)
	before := OpenHandles()

	eh := mustNew(t, `{"base":"EUR","quiet":true}`)

	var started, refused atomic.Int64
	for i := 0; i < raceIterations; i++ {
		// interval_ms:1 so that a loop which does escape is unmistakable: Run
		// refreshes immediately and then every millisecond.
		rh, err := NewRefresher(eh, `{"sources":"ecb","interval_ms":1,"quiet":true}`)
		if err != nil {
			t.Fatalf("NewRefresher on iteration %d: %v", i, err)
		}

		race(i%3,
			func() {
				if _, err := Call(rh, "start", `{}`); err != nil {
					refused.Add(1)
				} else {
					started.Add(1)
				}
			},
			func() { Close(rh) },
		)
	}
	Close(eh)

	if got := OpenHandles(); got != before {
		t.Errorf("%d handle(s) leaked over %d start/close races", got-before, raceIterations)
	}

	// Both orderings have to have happened, or this test is a green tick over
	// an untested window.
	if started.Load() == 0 || refused.Load() == 0 {
		t.Errorf("over %d races, start won %d and close won %d — one ordering never occurred, so this "+
			"test did not exercise the window it exists for",
			raceIterations, started.Load(), refused.Load())
	}

	// THE assertion. Every close has returned, and close waits for the loop it
	// cancelled, so the request count must now be frozen for good.
	frozen := net.n.Load()
	time.Sleep(500 * time.Millisecond)
	if got := net.n.Load(); got != frozen {
		t.Fatalf("this library made %d more request(s) in the half-second after the last openrate_close() "+
			"returned (%d -> %d). A refresh loop escaped its handle: nothing can stop it, openrate_close "+
			"is a permanent no-op for it, and openrate_open_handles() reports %d",
			got-frozen, frozen, got, OpenHandles()-before)
	}
}

// TestStopIsReversibleButCloseIsTerminal pins the distinction fix A rests on.
// "stop" is a host-callable pause and start-after-stop is documented as legal;
// close retires the handle. A single "closed" flag set by both would have made
// the first one poison the refresher, which is why the fix is a split and not a
// flag.
func TestStopIsReversibleButCloseIsTerminal(t *testing.T) {
	stubNetwork(t)
	eh := mustNew(t, `{"base":"EUR","quiet":true}`)
	defer Close(eh)

	rh, err := NewRefresher(eh, `{"sources":"ecb","interval_ms":50,"quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}
	for round := 0; round < 3; round++ {
		if _, err := Call(rh, "start", `{}`); err != nil {
			t.Fatalf("start after %d stop(s): %v — start-after-stop is part of the contract", round, err)
		}
		if _, err := Call(rh, "stop", `{}`); err != nil {
			t.Fatalf("stop: %v", err)
		}
	}

	Close(rh)
	_, err = Call(rh, "start", `{}`)
	if err == nil {
		t.Fatal("start succeeded on a closed handle; the loop it built would have no reachable stop")
	}
	if !strings.Contains(err.Error(), "not open") {
		t.Errorf("start on a closed handle said %q; the ABI's one answer for a retired handle is "+
			"\"handle N is not open\", and include/openrate.h promises a host it need match nothing else", err)
	}
}

// TestBuildingARefresherWhileTheEngineClosesLeavesNoOrphanHandle is finding B.
//
// openrate_refresher_new used to publish the new handle in the registry and
// only then append it to the engine's child list. An engine close landing
// between the two took its snapshot of the children before the append, so the
// refresher ended up live in the registry and invisible to the only thing that
// would ever have closed it. openrate_open_handles() then never returned to
// zero, which is the single question that entry point exists to answer.
func TestBuildingARefresherWhileTheEngineClosesLeavesNoOrphanHandle(t *testing.T) {
	stubNetwork(t)

	orphans, built, refused := 0, 0, 0
	for i := 0; i < orphanIterations; i++ {
		before := OpenHandles()
		eh := mustNew(t, `{"quiet":true}`)

		var rh uint64
		var nerr error
		race(i%3,
			func() { rh, nerr = NewRefresher(eh, `{"sources":"ecb","quiet":true}`) },
			func() { Close(eh) },
		)

		if nerr == nil {
			built++
		} else {
			refused++
		}

		// Either NewRefresher lost and returned an error, or it won and the
		// engine close took the child with it. There is no third outcome in
		// which a handle survives both.
		if got := OpenHandles(); got != before {
			orphans++
			// Put the registry back so the next iteration measures its own
			// race and not this one's residue.
			Close(rh)
			Close(eh)
		}
	}

	if built == 0 || refused == 0 {
		t.Errorf("over %d races NewRefresher succeeded %d time(s) and failed %d — one ordering never "+
			"occurred, so this test did not exercise the window it exists for", orphanIterations, built, refused)
	}
	if orphans != 0 {
		t.Fatalf("%d of %d refreshers (%.2f%%) outlived the engine they were built over: registered under "+
			"a handle the caller never received and absent from the child list the engine had already "+
			"snapshotted, so nothing can ever close them and openrate_open_handles() never returns to zero",
			orphans, orphanIterations, 100*float64(orphans)/float64(orphanIterations))
	}
}

// TestClosingARefresherDetachesItFromItsEngine is finding C.
//
// The engine's child list only ever grew. A refresher the host closed stayed on
// it, so "meta" kept reporting the fetch status of a handle that no longer
// exists, and an engine a host keeps for the life of the process accumulated
// one dead *refresherObj per open/close cycle.
func TestClosingARefresherDetachesItFromItsEngine(t *testing.T) {
	stubNetwork(t)
	eh := mustNew(t, `{"base":"EUR","quiet":true}`)
	defer Close(eh)

	rh, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}
	if _, err := Call(rh, "refresh", `{"timeout_ms":5000}`); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// While it is open, meta reports it. Without this half the test would pass
	// on an engine that never reported anything at all.
	if n := metaSourceCount(t, eh); n != 1 {
		t.Fatalf("meta reports %d source(s) while one refresher is open, want 1", n)
	}

	Close(rh)

	if n := metaSourceCount(t, eh); n != 0 {
		t.Errorf("meta still reports %d source(s) after the refresher that owned them was closed; a host "+
			"reading /meta is told about a handle it can no longer address, and the status will never "+
			"change again", n)
	}

	// The list itself, not just what meta prints: this is the leak an engine
	// held for the life of the process would accumulate.
	const cycles = 50
	for i := 0; i < cycles; i++ {
		h, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
		if err != nil {
			t.Fatalf("NewRefresher on cycle %d: %v", i, err)
		}
		Close(h)
	}
	eo := engineFor(t, eh)
	eo.mu.Lock()
	held := len(eo.refreshers)
	eo.mu.Unlock()
	if held != 0 {
		t.Errorf("the engine still holds %d refresher object(s) after %d open/close cycles; closing a "+
			"refresher frees its handle but not its memory, so a long-lived engine grows without bound",
			held, cycles)
	}
}

// wrapsToOneMS is 1 + 2^58 milliseconds — about nine million years. Multiplied
// by time.Millisecond in int64 nanoseconds it wraps to exactly 1ms, which is
// the whole of finding D: the overflow does not produce a nonsense value a
// caller would notice, it produces the fastest loop the library can run.
// It is a var and not a const on purpose: as a constant, Go computes the
// multiply in exact arithmetic at compile time and refuses to build ("overflows
// int64"), which hides the very behaviour a host gets at run time.
var wrapsToOneMS int64 = 1 + (1 << 58)

// TestMillisecondConfigCannotOverflowIntoATightLoop is finding D.
func TestMillisecondConfigCannotOverflowIntoATightLoop(t *testing.T) {
	net := stubNetwork(t)

	// The fixture has to still wrap, or every case below is asserting that a
	// harmless number is rejected.
	if got := time.Duration(wrapsToOneMS) * time.Millisecond; got != time.Millisecond {
		t.Fatalf("time.Duration(%d) * time.Millisecond is %v, not 1ms: this fixture no longer "+
			"demonstrates the overflow and the cases below prove nothing", wrapsToOneMS, got)
	}

	eh := mustNew(t, `{"quiet":true}`)
	defer Close(eh)

	t.Run("interval_ms", func(t *testing.T) {
		cfg := fmt.Sprintf(`{"sources":"ecb","interval_ms":%d,"quiet":true}`, wrapsToOneMS)
		rh, err := NewRefresher(eh, cfg)
		if err == nil {
			// Show what was actually configured rather than asserting it, so
			// the failure is a measurement and not an opinion.
			was := net.n.Load()
			_, _ = Call(rh, "start", `{}`)
			time.Sleep(200 * time.Millisecond)
			_, _ = Call(rh, "stop", `{}`)
			fetched := net.n.Load() - was
			Close(rh)
			t.Fatalf("interval_ms=%d (about nine million years) was accepted, and the loop built from it "+
				"made %d fetches in 200ms: the multiply by time.Millisecond wrapped to 1ms and turned "+
				"the most conservative cadence a host can ask for into the most aggressive one",
				wrapsToOneMS, fetched)
		}
		if !strings.Contains(err.Error(), "interval_ms") {
			t.Errorf("the error is %q; it should name interval_ms so the host knows which field to fix", err)
		}
	})

	t.Run("fetch_timeout_ms", func(t *testing.T) {
		cfg := fmt.Sprintf(`{"sources":"ecb","fetch_timeout_ms":%d,"quiet":true}`, wrapsToOneMS)
		if _, err := NewRefresher(eh, cfg); err == nil {
			t.Fatalf("fetch_timeout_ms=%d was accepted; it wraps to a 1ms bound on every source fetch, "+
				"so every refresh fails and the reason is nowhere in the config the host wrote", wrapsToOneMS)
		} else if !strings.Contains(err.Error(), "fetch_timeout_ms") {
			t.Errorf("the error is %q; it should name fetch_timeout_ms", err)
		}
	})

	t.Run("negative values wrap too", func(t *testing.T) {
		cfg := fmt.Sprintf(`{"sources":"ecb","interval_ms":%d,"quiet":true}`, -wrapsToOneMS)
		if _, err := NewRefresher(eh, cfg); err == nil {
			t.Errorf("interval_ms=%d was accepted; the bound has to be symmetric because a negative "+
				"out-of-range value overflows exactly as freely as a positive one", -wrapsToOneMS)
		}
	})

	t.Run("the largest representable value is still accepted", func(t *testing.T) {
		// The guard must be a bound, not a ban on large numbers.
		cfg := fmt.Sprintf(`{"sources":"ecb","interval_ms":%d,"fetch_timeout_ms":%d,"quiet":true}`,
			maxDurationMS, maxDurationMS)
		rh, err := NewRefresher(eh, cfg)
		if err != nil {
			t.Fatalf("interval_ms=%d is the largest value a duration can hold and was refused: %v",
				maxDurationMS, err)
		}
		Close(rh)
	})
}

// TestARequestTimeoutTooLargeToRepresentIsClampedNotWrapped is finding D on the
// per-call path. timeout_ms takes the same multiply, and wrapping it turns a
// caller's generous deadline into a 1ms one — reported back as "context
// deadline exceeded", which reads as the fetch having failed.
func TestARequestTimeoutTooLargeToRepresentIsClampedNotWrapped(t *testing.T) {
	stubNetwork(t)
	eh := mustNew(t, `{"base":"USD","quiet":true}`)
	defer Close(eh)
	rh, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}
	defer Close(rh)

	// Nothing is refreshing, so "ready" waits. With the multiply wrapped it
	// would instead give up after 1ms.
	done := make(chan error, 1)
	go func() {
		_, err := Call(rh, "ready", fmt.Sprintf(`{"timeout_ms":%d}`, wrapsToOneMS))
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("ready with timeout_ms=%d (about nine million years) returned within a moment: %v — "+
			"the deadline wrapped to 1ms and the caller is told their fetch timed out", wrapsToOneMS, err)
	case <-time.After(250 * time.Millisecond):
	}

	// Unblock it rather than leaving a goroutine parked for the rest of the
	// run: loading rates is what "ready" is waiting for.
	if _, err := Call(eh, "load", testEdges); err != nil {
		t.Fatalf("load: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ready returned %v after the engine was loaded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ready did not return after the engine was loaded")
	}
}

// metaSourceCount is how many entries "meta" reports under "sources" — the same
// field GET /api/v1/meta publishes.
func metaSourceCount(t *testing.T, engineHandle uint64) int {
	t.Helper()
	out, err := Call(engineHandle, "meta", `{}`)
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	var m struct {
		Sources []json.RawMessage `json:"sources"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	return len(m.Sources)
}
