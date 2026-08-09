package ratelimit

import (
	"fmt"
	"testing"
	"time"
)

// These cover the two halves of "what counts as one client": the bucket KEY
// (which addresses share a bucket) and the bucket MAP (how many buckets can
// exist, and what may be thrown away to stay under that ceiling).

var bucketBase = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// ─── bucket key ──────────────────────────────────────────────────────────────

// An IPv4-mapped IPv6 address is the same client as the plain IPv4 address —
// which form arrives depends on whether the listener is dual-stack, not on the
// caller. Two buckets for one client is two budgets for one client.
func TestIPv4MappedIPv6SharesTheIPv4Bucket(t *testing.T) {
	l := pinned(60, 1, bucketBase) // burst 1: the same bucket's 2nd request is denied
	defer l.Stop()

	if !l.Allow("1.2.3.4") {
		t.Fatal("first request from 1.2.3.4 must pass")
	}
	if l.Allow("::ffff:1.2.3.4") {
		t.Fatal("::ffff:1.2.3.4 is the same client as 1.2.3.4 and must share its bucket")
	}
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("the two forms created %d buckets, want 1", n)
	}
}

// The reviewer's scenario at the limiter level: 1000 distinct addresses inside
// one /64 at rpm=1 (burst 1) previously gave 1000 allowed and 0 blocked.
func TestOneIPv6SlashSixtyFourIsOneBucket(t *testing.T) {
	const n = 1000
	l := pinned(1, 1, bucketBase)
	defer l.Stop()

	seen := map[string]bool{}
	allowed, blocked := 0, 0
	for i := range n {
		addr := fmt.Sprintf("2001:db8:1:1:%x:%x:%x:%x", i, i*7+1, i*13+2, i*31+3)
		seen[addr] = true
		if l.Allow(addr) {
			allowed++
		} else {
			blocked++
		}
	}
	// Coverage counts: the loop really did run over 1000 DISTINCT addresses, and
	// every one of them was classified.
	if len(seen) != n {
		t.Fatalf("generated %d distinct addresses, want %d — the scenario is not being exercised", len(seen), n)
	}
	if allowed+blocked != n {
		t.Fatalf("classified %d requests, want %d", allowed+blocked, n)
	}
	if allowed != 1 || blocked != n-1 {
		t.Fatalf("one /64 at rpm=1 burst=1: %d allowed / %d blocked, want 1 / %d — "+
			"the /64 is being bucketed per address, so the limit never binds", allowed, blocked, n-1)
	}
	l.mu.Lock()
	buckets := len(l.buckets)
	l.mu.Unlock()
	if buckets != 1 {
		t.Fatalf("one /64 produced %d buckets, want 1", buckets)
	}
}

// The other direction: aggregation must stop at /64. Two different /64s are two
// different customers and must not share a budget.
func TestDistinctIPv6SlashSixtyFoursGetTheirOwnBuckets(t *testing.T) {
	l := pinned(60, 1, bucketBase)
	defer l.Stop()

	if !l.Allow("2001:db8:1:1::9") {
		t.Fatal("first /64 must pass")
	}
	if !l.Allow("2001:db8:1:2::9") {
		t.Fatal("a different /64 is a different client and must have its own bucket")
	}
	if l.Allow("2001:db8:1:2::abcd") {
		t.Fatal("the second /64's own budget must still bind")
	}
}

// IPv4 stays per-address: a single v4 address is routinely a whole NAT'd office,
// so aggregating it any coarser would limit unrelated people together.
func TestIPv4AddressesKeepIndependentBuckets(t *testing.T) {
	l := pinned(60, 1, bucketBase)
	defer l.Stop()

	if !l.Allow("198.51.100.4") {
		t.Fatal("first address must pass")
	}
	if !l.Allow("198.51.100.5") {
		t.Fatal("a neighbouring IPv4 address must not be aggregated into the same bucket")
	}
}

// A key that is not an address at all is used verbatim, so a caller using this
// limiter for something other than IPs is unaffected.
func TestNonAddressKeysArePassedThrough(t *testing.T) {
	if got := bucketKey("tenant-42"); got != "tenant-42" {
		t.Fatalf("bucketKey(%q) = %q, want it unchanged", "tenant-42", got)
	}
	if got := bucketKey("fe80::1%eth0"); got != "fe80::/64" {
		t.Fatalf("bucketKey with a zone = %q, want fe80::/64 — a varying zone must not mint buckets", got)
	}
}

// ─── bucket map ceiling ──────────────────────────────────────────────────────

// The map must not grow with attacker-chosen keys. The idle sweep does not bound
// it: keys can be minted far faster than 15 minutes.
func TestBucketMapIsCappedUnderAKeyFlood(t *testing.T) {
	l := pinned(60, 4, bucketBase)
	defer l.Stop()

	const flood = maxBuckets * 2
	peak := 0
	for i := range flood {
		l.Allow(fmt.Sprintf("2001:db8:%x:%x::1", i>>16, i&0xffff))
		l.mu.Lock()
		if n := len(l.buckets); n > peak {
			peak = n
		}
		l.mu.Unlock()
	}
	if peak > maxBuckets {
		t.Fatalf("bucket map peaked at %d entries, above the %d cap", peak, maxBuckets)
	}
	if peak != maxBuckets {
		t.Fatalf("bucket map peaked at %d, want it to reach the %d cap — the flood is not exercising the ceiling", peak, maxBuckets)
	}
}

// The eviction policy's whole point. An attacker who has spent its budget must
// not get it back by flooding the map until its own bucket falls off the end —
// which is exactly what a plain least-recently-used eviction would give it,
// because the drained bucket is the one it stops touching.
//
// rpm=1/burst=30 makes the two states distinguishable: 120 s refills a drained
// bucket by only 2 tokens while a nearly-full one reaches the ceiling.
func TestEvictionNeverRefundsAnOverBudgetBucket(t *testing.T) {
	at := bucketBase
	l := newWithClock(1, 30, func() time.Time { return at })
	defer l.Stop()
	l.maxBuckets = 4

	const attacker = "203.0.113.9"
	// Spend the whole budget at t0, then go quiet — the naive-LRU bait.
	for i := range 30 {
		if !l.Allow(attacker) {
			t.Fatalf("draining request %d/30 must be allowed", i+1)
		}
	}
	if l.Allow(attacker) {
		t.Fatal("the attacker's bucket must be exhausted after 30 requests")
	}

	// Three other clients, touched one second later so the attacker's bucket is
	// strictly the least-recently-used entry in the map.
	at = bucketBase.Add(time.Second)
	for _, k := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		if !l.Allow(k) {
			t.Fatalf("filler %s must be allowed", k)
		}
	}
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n != 4 {
		t.Fatalf("setup put %d buckets in the map, want exactly the cap (4)", n)
	}

	// Two minutes on: the fillers have refilled to the 30-token ceiling, the
	// attacker has earned 2 tokens. A new client now forces an eviction.
	at = bucketBase.Add(120 * time.Second)
	if !l.Allow("192.0.2.77") {
		t.Fatal("the new client must be served")
	}

	// The attacker's allowance must be the 2 tokens it earned, not a fresh 30.
	refund := 0
	for range 30 {
		if !l.Allow(attacker) {
			break
		}
		refund++
	}
	if refund != 2 {
		t.Fatalf("after the flood the attacker got %d requests through, want 2 (the tokens 120 s actually earned) — "+
			"eviction handed back an allowance", refund)
	}
}

// When the map is at the cap and nothing in it is safe to evict, new keys share
// one overflow budget rather than growing the map or displacing anyone.
func TestOverflowBucketWhenNothingIsEvictable(t *testing.T) {
	l := pinned(60, 3, bucketBase) // frozen clock: no bucket ever refills to full
	defer l.Stop()
	l.maxBuckets = 2

	for _, k := range []string{"10.0.0.1", "10.0.0.2"} {
		if !l.Allow(k) {
			t.Fatalf("%s must be allowed", k)
		}
	}

	allowed := 0
	const newcomers = 10
	for i := range newcomers {
		if l.Allow(fmt.Sprintf("10.9.%d.%d", i/256, i%256)) {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("newcomers at the cap got %d requests through, want exactly burst (3) from the shared overflow bucket", allowed)
	}
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n != 2 {
		t.Fatalf("map holds %d buckets, want it pinned at the cap (2)", n)
	}
}

// The idle sweeper is an eviction path too, so it carries the same rule: an idle
// bucket that has NOT refilled to full must survive, or a client resets its
// budget by pausing for the sweep interval.
func TestSweepKeepsADrainedButIdleBucket(t *testing.T) {
	l := newWithClock(1, 30, func() time.Time { return bucketBase }) // rate 1/min, burst 30
	defer l.Stop()

	l.mu.Lock()
	l.buckets["drained"] = &bucket{tokens: 0, last: bucketBase.Add(-20 * time.Minute)}
	l.buckets["refilled"] = &bucket{tokens: 29, last: bucketBase.Add(-20 * time.Minute)}
	l.mu.Unlock()

	l.sweep(bucketBase)

	l.mu.Lock()
	_, drainedOK := l.buckets["drained"]
	_, refilledOK := l.buckets["refilled"]
	l.mu.Unlock()

	if !drainedOK {
		t.Error("an idle bucket that has only earned back 20 of 30 tokens must survive the sweep")
	}
	if refilledOK {
		t.Error("an idle bucket that has refilled to full holds no state and must be swept")
	}
}
