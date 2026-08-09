package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pinned returns a Limiter whose clock is frozen at the given instant.
// Using newWithClock (rather than assigning l.now after construction) ensures
// there is no data race between the gc goroutine and the test.
func pinned(rpm, burst int, at time.Time) *Limiter {
	return newWithClock(rpm, burst, func() time.Time { return at })
}

// ─── Token-bucket algorithm correctness ─────────────────────────────────────

// TestTokenBucketRefillProportional verifies that tokens are added proportional
// to elapsed time and that a fully-drained bucket recovers after one refill
// period.
func TestTokenBucketRefillProportional(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// rpm=60 → rate=1 token/s; burst=5
	at := base
	l := newWithClock(60, 5, func() time.Time { return at })
	defer l.Stop()

	// Drain: burst=5 calls all pass.
	for i := range 5 {
		if !l.Allow("ip") {
			t.Fatalf("drain call %d/5 must be allowed", i+1)
		}
	}
	if l.Allow("ip") {
		t.Fatal("6th call on empty bucket must be denied")
	}

	// Advance exactly 1 s → 1 token refilled.
	at = base.Add(time.Second)
	if !l.Allow("ip") {
		t.Fatal("after 1 s refill one call must pass")
	}
	if l.Allow("ip") {
		t.Fatal("second call immediately after single-token refill must be denied")
	}
}

// TestTokenBucketBurstExact verifies that exactly burst requests pass with
// frozen time and the very next one is denied.
func TestTokenBucketBurstExact(t *testing.T) {
	const burst = 7
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := pinned(60, burst, base)
	defer l.Stop()

	for i := range burst {
		if !l.Allow("k") {
			t.Fatalf("request %d/%d must be allowed", i+1, burst)
		}
	}
	if l.Allow("k") {
		t.Fatalf("request %d must be denied — bucket exhausted", burst+1)
	}
}

// TestTokenBucketOverflowCapped verifies that a very long idle period does not
// accumulate tokens beyond the burst ceiling.
func TestTokenBucketOverflowCapped(t *testing.T) {
	const burst = 4
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := base
	l := newWithClock(60, burst, func() time.Time { return at })
	defer l.Stop()

	l.Allow("k") // seed the bucket (tokens = burst-1 = 3)

	// Advance 24 h — without capping this would add 86 400 tokens.
	at = base.Add(24 * time.Hour)

	// Exactly burst calls must pass after the cap-to-burst reset.
	for i := range burst {
		if !l.Allow("k") {
			t.Fatalf("call %d/%d must be allowed after overflow-cap (burst=%d)", i+1, burst, burst)
		}
	}
	if l.Allow("k") {
		t.Fatal("(burst+1)th call must be denied — overflow is capped to burst")
	}
}

// TestTokenBucketUnderflowSafe verifies that repeated denials do not corrupt
// the bucket and that it recovers correctly once tokens refill.
func TestTokenBucketUnderflowSafe(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// rpm=120 → rate=2 tokens/s; burst=2
	at := base
	l := newWithClock(120, 2, func() time.Time { return at })
	defer l.Stop()

	l.Allow("k") // tokens → 1
	l.Allow("k") // tokens → 0

	// Ten consecutive denials must not drive tokens below zero.
	for range 10 {
		if l.Allow("k") {
			t.Fatal("exhausted bucket must not allow requests")
		}
	}

	// Advance 1 s → 2 tokens refilled (rate=2/s).
	at = base.Add(time.Second)
	for i := range 2 {
		if !l.Allow("k") {
			t.Fatalf("recovered request %d must pass", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("third request after 2-token refill must be denied")
	}
}

// TestBucketsArePerKey verifies that different client keys maintain completely
// independent token buckets.
func TestBucketsArePerKey(t *testing.T) {
	const burst = 1
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := pinned(60, burst, base)
	defer l.Stop()

	l.Allow("a") // exhaust a's bucket
	if l.Allow("a") {
		t.Fatal("a's second request must be denied")
	}
	// b has its own bucket and must be allowed.
	if !l.Allow("b") {
		t.Fatal("b has an independent bucket and must be allowed")
	}
}

// TestConcurrentBurstEnforced fires 3×burst goroutines simultaneously against a
// frozen-time limiter and asserts that exactly burst of them are allowed.
// Run with -race to verify there are no data races.
func TestConcurrentBurstEnforced(t *testing.T) {
	const burst = 10
	pinnedTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := newWithClock(60, burst, func() time.Time { return pinnedTime })
	defer l.Stop()

	const total = burst * 3
	start := make(chan struct{})
	var wg sync.WaitGroup
	var allowed atomic.Int64

	for range total {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if l.Allow("shared") {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := int(allowed.Load()); got != burst {
		t.Errorf("concurrent burst: %d allowed, want exactly %d", got, burst)
	}
}

// TestGracefulDegradationUnderSaturation fires requests faster than the refill
// rate and verifies that only burst pass when each refill is sub-token.
//
//	rpm=60 → rate=1 token/s, burst=2, advance=100ms/call → refill=0.1 tokens/call.
//	Call 1: bucket created (tokens=1), allowed.
//	Call 2: refill 0.1 → tokens=1.1 ≥ 1, allowed; tokens=0.1.
//	Calls 3–10: refill 0.1 each → tokens<1, all denied.
func TestGracefulDegradationUnderSaturation(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	current := base
	l := newWithClock(60, 2, func() time.Time { return current })
	defer l.Stop()

	allowed := 0
	for range 10 {
		if l.Allow("k") {
			allowed++
		}
		current = current.Add(100 * time.Millisecond)
	}
	if allowed != 2 {
		t.Errorf("saturation: %d allowed, want 2 (sub-token refill, burst cap)", allowed)
	}
}

// ─── GC / sweep correctness ──────────────────────────────────────────────────

// TestSweepEvictsStale verifies that sweep removes buckets idle for more than
// 15 minutes and retains buckets with recent activity.
func TestSweepEvictsStale(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	l := newWithClock(60, 10, func() time.Time { return base })
	defer l.Stop()

	// Seed a recent bucket at base.
	l.Allow("recent")

	// Manually insert a stale bucket (20 min old — past the 15-min cutoff).
	l.mu.Lock()
	l.buckets["stale"] = &bucket{tokens: 5, last: base.Add(-20 * time.Minute)}
	l.mu.Unlock()

	// A second recent bucket should also survive.
	l.Allow("also-recent")

	// Sweep at base: cutoff = base - 15 min.
	l.sweep(base)

	l.mu.Lock()
	_, recentOK := l.buckets["recent"]
	_, staleOK := l.buckets["stale"]
	_, alsoOK := l.buckets["also-recent"]
	l.mu.Unlock()

	if !recentOK {
		t.Error("recent bucket must survive sweep")
	}
	if !alsoOK {
		t.Error("also-recent bucket must survive sweep")
	}
	if staleOK {
		t.Error("stale bucket (20 min old) must be evicted by sweep")
	}
}

// TestSweepRetainsExactBoundary verifies that a bucket last used exactly at the
// cutoff (b.last == cutoff) is NOT evicted — only strictly-before is stale.
func TestSweepRetainsExactBoundary(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cutoff := base.Add(-15 * time.Minute) // sweep(base) uses this as its cutoff
	l := newWithClock(60, 10, func() time.Time { return base })
	defer l.Stop()

	l.mu.Lock()
	l.buckets["boundary"] = &bucket{tokens: 5, last: cutoff} // exactly at cutoff
	l.mu.Unlock()

	l.sweep(base)

	l.mu.Lock()
	_, ok := l.buckets["boundary"]
	l.mu.Unlock()

	if !ok {
		t.Error("bucket at exact cutoff boundary must not be evicted")
	}
}

// ─── Middleware shape ────────────────────────────────────────────────────────

// TestMiddleware429Shape verifies the HTTP shape of a rate-limited response:
// status 429, JSON Content-Type, and a positive Retry-After header.
func TestMiddleware429Shape(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := pinned(60, 1, base)
	defer l.Stop()
	l.Allow("10.0.0.1") // exhaust the one burst token

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	l.Middleware(inner).ServeHTTP(rr, r)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rr.Code)
	}
	ra, err := strconv.Atoi(rr.Header().Get("Retry-After"))
	if err != nil || ra <= 0 {
		t.Errorf("Retry-After = %q; want a positive integer", rr.Header().Get("Retry-After"))
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// TestMiddlewarePassThrough verifies that an allowed request reaches the inner
// handler and gets a 200 response.
func TestMiddlewarePassThrough(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l := pinned(60, 10, base)
	defer l.Stop()

	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.1:5000"
	l.Middleware(inner).ServeHTTP(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !reached {
		t.Error("inner handler was not invoked for an allowed request")
	}
}

// ─── XFF / proxy edge cases ─────────────────────────────────────────────────

// TestXFFMultipleHops verifies that only the left-most XFF entry is used when
// the direct peer is a trusted proxy.
func TestXFFMultipleHops(t *testing.T) {
	l := New(60, 10, "10.0.0.0/8")
	defer l.Stop()
	got := l.ClientIP(req("10.1.2.3:443", "203.0.113.5, 10.99.0.1, 10.1.2.3"))
	if got != "203.0.113.5" {
		t.Errorf("multi-hop XFF: want 203.0.113.5 (left-most), got %q", got)
	}
}

// TestTrustedProxyNoXFFUsesRemoteAddr verifies that a trusted proxy without an
// XFF header falls back to RemoteAddr without panicking or returning empty.
func TestTrustedProxyNoXFFUsesRemoteAddr(t *testing.T) {
	l := New(60, 10, "10.0.0.0/8")
	defer l.Stop()
	got := l.ClientIP(req("10.1.2.3:443", ""))
	if got != "10.1.2.3" {
		t.Errorf("trusted peer, no XFF: want 10.1.2.3, got %q", got)
	}
}

// TestIPv6TrustedProxyCIDR verifies that IPv6 CIDR entries work in the trusted
// proxy list.
func TestIPv6TrustedProxyCIDR(t *testing.T) {
	l := New(60, 10, "::1/128")
	defer l.Stop()
	got := l.ClientIP(req("[::1]:443", "2001:db8::1"))
	if got != "2001:db8::1" {
		t.Errorf("IPv6 trusted peer: want 2001:db8::1, got %q", got)
	}
}

// TestParseProxiesSkipsInvalidEntries verifies that malformed proxy specs are
// silently ignored and valid entries still apply.
func TestParseProxiesSkipsInvalidEntries(t *testing.T) {
	l := New(60, 10, "not-an-ip", "300.300.300.300", "10.0.0.0/8")
	defer l.Stop()
	if !l.trustsPeer("10.0.0.1") {
		t.Error("10.0.0.1 must be trusted (10.0.0.0/8 is the valid entry)")
	}
	if l.trustsPeer("9.9.9.9") {
		t.Error("9.9.9.9 must not be trusted (invalid entries skipped)")
	}
}

// TestMalformedRemoteAddrFallback verifies that a RemoteAddr without a port
// does not panic or return garbage (net.SplitHostPort error path).
func TestMalformedRemoteAddrFallback(t *testing.T) {
	l := New(60, 10)
	defer l.Stop()
	// RemoteAddr has no port — SplitHostPort will fail; fallback is the raw value.
	r := &http.Request{RemoteAddr: "192.0.2.9", Header: http.Header{}}
	got := l.ClientIP(r)
	if got != "192.0.2.9" {
		t.Errorf("malformed RemoteAddr: want 192.0.2.9, got %q", got)
	}
}
