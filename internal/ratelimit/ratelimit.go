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
}

// New builds a limiter allowing rpm requests/minute with the given burst.
func New(rpm, burst int) *Limiter {
	l := &Limiter{
		buckets: map[string]*bucket{},
		rate:    float64(rpm) / 60.0,
		burst:   float64(burst),
		now:     time.Now,
	}
	go l.gc()
	return l
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

// gc evicts idle buckets so the map can't grow unbounded.
func (l *Limiter) gc() {
	t := time.NewTicker(10 * time.Minute)
	for range t.C {
		l.mu.Lock()
		cutoff := l.now().Add(-15 * time.Minute)
		for k, b := range l.buckets {
			if b.last.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// Middleware rate-limits by client IP, returning 429 with Retry-After when a
// client exceeds its budget.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(ClientIP(r)) {
			w.Header().Set("Retry-After", strconv.Itoa(int(1/l.rate)+1))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited — slow down. For higher limits use Vulos Cloud."}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP extracts the caller's IP, trusting the left-most X-Forwarded-For hop
// when present (set by a fronting proxy / Vulos Cloud), else RemoteAddr.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
