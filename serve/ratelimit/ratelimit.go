// Package ratelimit is a small per-IP token-bucket limiter — best-effort
// anti-scraping for the public API so a single client can't harvest the whole
// rate set in a tight loop.
package ratelimit

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// How much of a client address identifies "one client" for rate-limiting.
//
// ipv4BucketBits is /32 — the whole address — because a single IPv4 address is
// the smallest thing an operator hands out and is routinely shared by a whole
// CGNAT'd or office-NAT'd population. Aggregating IPv4 any coarser would limit
// unrelated people together.
//
// ipv6BucketBits is /64 because IPv6 does not work that way: SLAAC gives every
// LAN a /64 and every host on it as many addresses as it cares to generate, so
// one machine owns 2^64 addresses of its own. Bucketing IPv6 per /128 — what
// this limiter did before — means the limit never binds on a v6 client: 1000
// requests from one /64 at rpm=1 all passed.
//
// /64 rather than /56 or /48: a /64 is the smallest unit a single party can be
// assumed to control entirely, and the largest unit that is unlikely to span
// unrelated customers. Providers that assign each VM a fragment of a shared /64
// (DigitalOcean hands out a /124 per droplet) already put strangers in one
// bucket at /64; going to /56 or /48 would sweep in whole neighbouring
// allocations and rate-limit bystanders for an attacker's traffic. The residual
// is accepted knowingly: a client holding a /56 still gets 256 buckets, which is
// a bounded 256× budget rather than the unbounded one /128 gave it.
const (
	ipv4BucketBits = 32
	ipv6BucketBits = 64
)

// maxBuckets caps the live bucket map. The 15-minute idle sweep alone does not
// bound it: an attacker cycling source prefixes (or a botnet) mints keys far
// faster than the sweeper reclaims them, so the map is a memory-exhaustion
// vector keyed by attacker-chosen input. At ~130 bytes per entry this ceiling is
// on the order of 10 MB, and it is far more distinct client prefixes than a
// single openrate deployment legitimately serves.
const maxBuckets = 65536

// evictionSample is how many buckets are examined to choose an eviction victim.
// A full scan at the cap would be O(maxBuckets) work on every request of a key
// flood — a DoS of its own — so a small random sample is taken instead (Go
// randomises map iteration order, which is where the sample comes from).
const evictionSample = 8

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter refills each IP's bucket at rate tokens/sec up to burst.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	// overflow is the shared fallback bucket used when the map is at maxBuckets
	// and no entry can be evicted without handing someone a fresh budget. See
	// Allow.
	overflow   *bucket
	maxBuckets int
	rate       float64
	burst      float64
	now        func() time.Time
	trusted    []*net.IPNet  // proxies whose X-Forwarded-For we honor
	done       chan struct{} // closed by Stop to terminate the GC goroutine
}

// New builds a limiter allowing rpm requests/minute with the given burst.
// Both rpm and burst are clamped to a minimum of 1 to prevent division-by-zero
// in the Retry-After calculation.
//
// trustedProxies lists the downstream addresses (IPs or CIDRs, e.g.
// "10.0.0.0/8" or "203.0.113.4") permitted to set X-Forwarded-For. Only when a
// request's RemoteAddr falls in this set is the client IP read from XFF;
// otherwise RemoteAddr is used. With no trusted proxies (the default) XFF is
// never trusted, so a directly-exposed client can't mint fresh buckets by
// rotating the header. Invalid entries are ignored.
//
// When a trusted proxy is the direct peer, the client IP is selected as the
// RIGHT-most XFF entry that is not itself a configured trusted proxy — because
// standard reverse proxies (nginx $proxy_add_x_forwarded_for, Cloudflare)
// APPEND the address they observed rather than replace, so the genuine client
// sits to the right and the forgeable, client-supplied hops sit to the left.
// This prevents a proxied client from rotating a left-most XFF value to mint a
// fresh rate-limit bucket per request.
//
// Call Stop when the Limiter is no longer needed to release its background
// goroutine and ticker.
func New(rpm, burst int, trustedProxies ...string) *Limiter {
	return newWithClock(rpm, burst, time.Now, trustedProxies...)
}

// newWithClock is the internal constructor used by New and tests. Accepting the
// clock at construction time (rather than allowing post-construction field
// assignment) eliminates the data race that a concurrent gc goroutine would
// otherwise create against a test reassigning l.now.
func newWithClock(rpm, burst int, now func() time.Time, trustedProxies ...string) *Limiter {
	if rpm < 1 {
		rpm = 1
	}
	if burst < 1 {
		burst = 1
	}
	l := &Limiter{
		buckets:    map[string]*bucket{},
		maxBuckets: maxBuckets,
		rate:       float64(rpm) / 60.0,
		burst:      float64(burst),
		now:        now,
		trusted:    parseProxies(trustedProxies),
		done:       make(chan struct{}),
	}
	go l.gc()
	return l
}

// Stop terminates the background GC goroutine and its ticker. It is safe to
// call Stop more than once.
func (l *Limiter) Stop() {
	select {
	case <-l.done: // already stopped
	default:
		close(l.done)
	}
}

// parseProxies turns IP and CIDR strings into networks. A bare IP becomes a
// host route (/32 or /128). Unparseable entries are skipped.
func parseProxies(specs []string) []*net.IPNet {
	var nets []*net.IPNet
	for _, s := range specs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(s); err == nil {
			nets = append(nets, n)
			continue
		}
		if ip := net.ParseIP(s); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return nets
}

// Allow reports whether a request from key may proceed, consuming one token.
//
// key is normalized to a bucket first (see bucketKey): an IP address is bucketed
// by prefix, so a client cannot mint a fresh budget by moving within its own
// allocation, and anything that is not an IP is used verbatim.
//
// The bucket map is capped at maxBuckets. At the cap a victim is chosen from a
// small random sample — but ONLY among buckets that are refilled to full, whose
// state is byte-for-byte what a newly created bucket would hold. That is the
// whole eviction policy and the reason for it: evicting a full bucket gives its
// owner nothing, so eviction can never be used to clear an over-budget
// attacker's own bucket and reset their allowance. A plain LRU would have
// exactly that flaw — an attacker over budget on one key sprays fresh keys until
// their own drained bucket falls off the end, then returns to it with a full
// allowance. Sampling means the choice is not attacker-steerable either.
//
// When nothing in the sample is full, the request is charged to a single shared
// overflow bucket instead of growing the map. Under a sustained key flood that
// makes newly-seen clients share one budget — a real degradation, and the
// deliberate trade for a hard memory ceiling that cannot be turned into free
// allowance.
func (l *Limiter) Allow(key string) bool {
	k := bucketKey(key)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if b, ok := l.buckets[k]; ok {
		return l.take(b, now)
	}
	if len(l.buckets) >= l.maxBuckets && !l.evictOne(now) {
		if l.overflow == nil {
			l.overflow = &bucket{tokens: l.burst, last: now}
		}
		return l.take(l.overflow, now)
	}
	l.buckets[k] = &bucket{tokens: l.burst - 1, last: now}
	return true
}

// bucketKey collapses a client address to the identity the limiter counts
// against. An IPv4-mapped IPv6 address (::ffff:1.2.3.4) is unmapped, so it lands
// in the same bucket as the plain 1.2.3.4 rather than a second one, and the
// result is masked to the per-family prefix above. Masking also drops any zone
// (netip.PrefixFrom does not carry one), so a varying fe80::1%zone cannot mint
// buckets either. A key that is not an IP at all — a caller using Limiter for
// something else, or a RemoteAddr that did not parse — is returned unchanged.
func bucketKey(key string) string {
	addr, err := netip.ParseAddr(key)
	if err != nil {
		return key
	}
	addr = addr.Unmap()
	bits := ipv6BucketBits
	if addr.Is4() {
		bits = ipv4BucketBits
	}
	p, err := addr.Prefix(bits)
	if err != nil { // unreachable: bits is always valid for the family
		return addr.String()
	}
	return p.String()
}

// take refills b to now and consumes a token if one is available.
func (l *Limiter) take(b *bucket, now time.Time) bool {
	b.tokens = l.tokensAt(b, now)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// tokensAt is b's token count refilled to now, capped at burst. It does not
// mutate b, so callers deciding *about* a bucket (eviction, sweeping) can ask
// without destroying the last-used time they are about to read.
func (l *Limiter) tokensAt(b *bucket, now time.Time) float64 {
	t := b.tokens + now.Sub(b.last).Seconds()*l.rate
	if t > l.burst {
		t = l.burst
	}
	return t
}

// full reports whether b has refilled to capacity, i.e. whether deleting it
// would change any answer this limiter gives. It is the safety condition on
// every eviction path in this package.
func (l *Limiter) full(b *bucket, now time.Time) bool {
	return l.tokensAt(b, now) >= l.burst
}

// evictOne deletes the least-recently-used FULL bucket among a bounded random
// sample and reports whether it deleted anything. Caller holds l.mu.
func (l *Limiter) evictOne(now time.Time) bool {
	var (
		victim string
		oldest time.Time
		found  bool
		seen   int
	)
	for k, b := range l.buckets {
		if seen >= evictionSample {
			break
		}
		seen++
		if !l.full(b, now) {
			continue // evicting it would hand its owner a fresh allowance
		}
		if !found || b.last.Before(oldest) {
			victim, oldest, found = k, b.last, true
		}
	}
	if !found {
		return false
	}
	delete(l.buckets, victim)
	return true
}

// sweep evicts per-IP buckets that have been idle for more than 15 minutes
// relative to now. It is called periodically by gc and is directly testable.
//
// Idleness alone is not sufficient: a bucket is only removed once it has also
// refilled to full, so the sweeper cannot hand back an allowance either. With
// any ordinary configuration 15 idle minutes refills far more than burst and the
// two conditions coincide, but a limiter configured with a large burst and a
// slow rate (rpm=1, burst=1000) has drained buckets that are still idle, and
// dropping those would let a client reset its budget by pausing.
func (l *Limiter) sweep(now time.Time) {
	cutoff := now.Add(-15 * time.Minute)
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.buckets {
		if b.last.Before(cutoff) && l.full(b, now) {
			delete(l.buckets, k)
		}
	}
}

// gc evicts idle buckets so the map can't grow unbounded. It stops when Stop
// closes the done channel.
func (l *Limiter) gc() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-t.C:
			l.sweep(l.now())
		}
	}
}

// Middleware rate-limits by client IP, returning 429 with Retry-After when a
// client exceeds its budget.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(l.ClientIP(r)) {
			w.Header().Set("Retry-After", strconv.Itoa(int(1/l.rate)+1))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited — slow down."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP extracts the caller's IP. X-Forwarded-For is honored only when the
// direct peer (RemoteAddr) is a configured trusted proxy; otherwise RemoteAddr
// is used and XFF is ignored entirely, which stops a directly-exposed attacker
// from rotating the header to get a fresh rate-limit bucket per request.
//
// When the peer is trusted, the client IP is the RIGHT-most XFF entry that is
// not itself a trusted proxy: reverse proxies append the address they observed,
// so walking from the right past the trusted-proxy hops yields the real client
// the outermost trusted proxy saw — a value the client cannot forge. Everything
// to the left of that boundary is client-supplied and forgeable, so it is never
// used. Blank entries are skipped; a malformed (non-trusted, unparseable) entry
// marks the untrusted boundary and causes a fail-safe fall back to RemoteAddr.
// If the header is absent or every hop is trusted, RemoteAddr is used.
//
// Header.VALUES, not Header.Get. XFF may legitimately arrive as several separate
// header lines rather than one comma-joined line — HAProxy's `option forwardfor`
// adds an occurrence rather than appending to an existing one — and Header.Get
// returns only the FIRST. A client that sends its own X-Forwarded-For line puts
// it first, so a Get-based walk would evaluate the attacker's line and never see
// the proxy's, handing back a forged address from the very code whose purpose is
// to find the unforgeable one. Joining every occurrence in order restores the
// right-to-left walk over the complete hop list.
func (l *Limiter) ClientIP(r *http.Request) string {
	host := remoteHost(r)
	if !l.trustsPeer(host) {
		return host
	}
	parts := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		p := strings.TrimSpace(parts[i])
		if p == "" {
			continue // tolerate stray/trailing commas
		}
		ip := net.ParseIP(p)
		if ip == nil {
			break // untrusted, forgeable boundary — stop and use the peer
		}
		if l.ipTrusted(ip) {
			continue // a trusted-proxy hop; keep walking left
		}
		return p // first genuine (non-trusted) client address from the right
	}
	return host
}

// trustsPeer reports whether the direct peer IP is in the trusted-proxy set.
func (l *Limiter) trustsPeer(host string) bool {
	if len(l.trusted) == 0 {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return l.ipTrusted(ip)
}

// ipTrusted reports whether ip falls within any configured trusted-proxy net.
func (l *Limiter) ipTrusted(ip net.IP) bool {
	for _, n := range l.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// remoteHost returns the IP portion of r.RemoteAddr (no port).
func remoteHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
