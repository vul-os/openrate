// Package ratelimit is a small per-IP token-bucket limiter — best-effort
// anti-scraping for the public API so a single client can't harvest the whole
// rate set in a tight loop. Heavier anti-abuse (API keys, per-plan quotas, WAF,
// CDN edge) belongs to Vulos Cloud, not the engine — see CLOUD.md.
package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter refills each IP's bucket at rate tokens/sec up to burst.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64
	burst   float64
	now     func() time.Time
	trusted []*net.IPNet // proxies whose X-Forwarded-For we honor
	done    chan struct{} // closed by Stop to terminate the GC goroutine
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
		buckets: map[string]*bucket{},
		rate:    float64(rpm) / 60.0,
		burst:   float64(burst),
		now:     now,
		trusted: parseProxies(trustedProxies),
		done:    make(chan struct{}),
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
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep evicts per-IP buckets that have been idle for more than 15 minutes
// relative to now. It is called periodically by gc and is directly testable.
func (l *Limiter) sweep(now time.Time) {
	cutoff := now.Add(-15 * time.Minute)
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
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
			_, _ = w.Write([]byte(`{"error":"rate limited — slow down. For higher limits use Vulos Cloud."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP extracts the caller's IP. The left-most X-Forwarded-For hop is
// honored only when the direct peer (RemoteAddr) is a configured trusted proxy;
// otherwise RemoteAddr is used. This stops a directly-exposed attacker from
// rotating XFF to get a fresh rate-limit bucket per request.
func (l *Limiter) ClientIP(r *http.Request) string {
	host := remoteHost(r)
	if l.trustsPeer(host) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
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
