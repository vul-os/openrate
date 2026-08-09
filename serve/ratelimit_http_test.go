package serve_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vul-os/openrate/serve"
)

// The rate limiter as the deployment actually runs it: through Server.Handler(),
// which is where guard() mounts it on /api/ paths. Driving the real handler is
// the only way to cover the whole chain (RemoteAddr → ClientIP → bucket key),
// and RemoteAddr can only be varied from inside the process — no real socket
// lets a test choose its own source address.

// limitedHandler is a rate-limited server at rpm=1 (burst = rpm/2+1 = 1), the
// reviewer's configuration.
func limitedHandler(t *testing.T) http.Handler {
	t.Helper()
	s := serve.New(populatedEngine(t, testEdges, 3), serve.Options{
		RateLimit: 1,
		Now:       func() time.Time { return apiTestTime },
	})
	t.Cleanup(func() { _ = s.Close() })
	return s.Handler()
}

func getFrom(h http.Handler, remoteAddr string) int {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
	r.RemoteAddr = remoteAddr
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr.Code
}

// The reviewer's scenario verbatim: 1000 requests from distinct addresses inside
// one /64 at rpm=1 gave 0 blocked, because every /128 got its own bucket. An
// IPv6 client can generate addresses inside its own /64 for free, so a per-/128
// limiter does not limit anything.
func TestIPv6SlashSixtyFourCannotOutrunTheAPIRateLimit(t *testing.T) {
	h := limitedHandler(t)

	const n = 1000
	seen := map[string]bool{}
	codes := map[int]int{}
	for i := range n {
		addr := fmt.Sprintf("[2001:db8:dead:beef:%x:%x:%x:%x]:443", i, i*3+1, i*5+2, i*7+3)
		seen[addr] = true
		codes[getFrom(h, addr)]++
	}

	// Coverage counts: 1000 genuinely distinct sources, every one classified — an
	// empty or collapsed loop cannot satisfy this vacuously.
	if len(seen) != n {
		t.Fatalf("generated %d distinct source addresses, want %d", len(seen), n)
	}
	if got := codes[http.StatusOK] + codes[http.StatusTooManyRequests]; got != n {
		t.Fatalf("classified %d of %d requests as 200/429 (all codes: %v)", got, n, codes)
	}
	if codes[http.StatusOK] != 1 || codes[http.StatusTooManyRequests] != n-1 {
		t.Fatalf("one /64 at rpm=1: %d served / %d limited, want 1 / %d (all codes: %v) — "+
			"the limiter is bucketing per /128 and never binds on an IPv6 client",
			codes[http.StatusOK], codes[http.StatusTooManyRequests], n-1, codes)
	}
}

// The same client reaching a dual-stack listener as ::ffff:1.2.3.4 must not get
// a second budget. Which form arrives is a property of the listener, not the
// caller.
func TestIPv4MappedIPv6DoesNotGetASecondBudget(t *testing.T) {
	h := limitedHandler(t)

	if got := getFrom(h, "1.2.3.4:1111"); got != http.StatusOK {
		t.Fatalf("first request from 1.2.3.4: status %d, want 200", got)
	}
	if got := getFrom(h, "[::ffff:1.2.3.4]:2222"); got != http.StatusTooManyRequests {
		t.Fatalf("::ffff:1.2.3.4 is the same client as 1.2.3.4: status %d, want 429", got)
	}
}

// The limiter must still be a LIMITER and not a blanket block: an unrelated
// client is served while the flooding one is throttled.
func TestUnrelatedClientsAreNotCaughtByAnotherPrefixesLimit(t *testing.T) {
	h := limitedHandler(t)

	if got := getFrom(h, "[2001:db8:1:1::1]:443"); got != http.StatusOK {
		t.Fatalf("first client: status %d, want 200", got)
	}
	if got := getFrom(h, "[2001:db8:1:1::2]:443"); got != http.StatusTooManyRequests {
		t.Fatalf("same /64: status %d, want 429", got)
	}
	if got := getFrom(h, "[2001:db8:1:2::1]:443"); got != http.StatusOK {
		t.Fatalf("a different /64 must have its own budget: status %d, want 200", got)
	}
	if got := getFrom(h, "203.0.113.7:80"); got != http.StatusOK {
		t.Fatalf("an unrelated IPv4 client must have its own budget: status %d, want 200", got)
	}
}
