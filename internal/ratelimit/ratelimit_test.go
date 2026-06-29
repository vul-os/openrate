package ratelimit

import (
	"net/http"
	"testing"
)

func req(remoteAddr, xff string) *http.Request {
	r := &http.Request{RemoteAddr: remoteAddr, Header: http.Header{}}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestClientIP_NoTrustIgnoresXFF(t *testing.T) {
	l := New(60, 10) // no trusted proxies
	defer l.Stop()
	got := l.ClientIP(req("203.0.113.9:5555", "1.2.3.4"))
	if got != "203.0.113.9" {
		t.Fatalf("untrusted peer XFF must be ignored: got %q, want RemoteAddr 203.0.113.9", got)
	}
}

func TestClientIP_TrustedProxyHonorsXFF(t *testing.T) {
	l := New(60, 10, "10.0.0.0/8")
	defer l.Stop()
	got := l.ClientIP(req("10.1.2.3:443", "1.2.3.4, 10.1.2.3"))
	if got != "1.2.3.4" {
		t.Fatalf("trusted peer: want left-most XFF 1.2.3.4, got %q", got)
	}
}

func TestClientIP_TrustedExactIP(t *testing.T) {
	l := New(60, 10, "192.0.2.7")
	defer l.Stop()
	if got := l.ClientIP(req("192.0.2.7:80", "8.8.8.8")); got != "8.8.8.8" {
		t.Fatalf("trusted exact IP: want 8.8.8.8, got %q", got)
	}
	// A different peer must not be trusted even with the same XFF present.
	if got := l.ClientIP(req("192.0.2.8:80", "8.8.8.8")); got != "192.0.2.8" {
		t.Fatalf("untrusted peer: want RemoteAddr 192.0.2.8, got %q", got)
	}
}

// TestRotatingXFFCannotMintBuckets is the security regression: a directly
// exposed client rotating XFF must keep hitting the same bucket (its RemoteAddr)
// and so still get rate limited.
func TestRotatingXFFCannotMintBuckets(t *testing.T) {
	l := New(60, 1) // burst 1: second request from same key is denied
	defer l.Stop()
	if !l.Allow(l.ClientIP(req("203.0.113.5:1", "9.9.9.1"))) {
		t.Fatal("first request should pass")
	}
	if l.Allow(l.ClientIP(req("203.0.113.5:1", "9.9.9.2"))) {
		t.Fatal("rotating XFF from an untrusted peer must not bypass the limit")
	}
}
