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

// TestClientIP_ProxiedForgedLeftmostIgnored is the config-conditional security
// regression: reverse proxies APPEND the real client, so with a trusted proxy
// as the peer, a client-supplied (forged) left-most XFF value must be ignored
// in favor of the right-most non-trusted entry — the real client the proxy saw.
func TestClientIP_ProxiedForgedLeftmostIgnored(t *testing.T) {
	l := New(60, 10, "10.0.0.0/8")
	defer l.Stop()
	// Peer is the trusted proxy; it appended the real client 1.2.3.4 on the
	// right. The attacker forged "6.6.6.6" as the left-most hop.
	if got := l.ClientIP(req("10.9.9.9:443", "6.6.6.6, 1.2.3.4")); got != "1.2.3.4" {
		t.Fatalf("forged left-most must be ignored: want real client 1.2.3.4, got %q", got)
	}
	// A multi-hop chain: two trusted proxies appended on the right, real client
	// 1.2.3.4 sits just left of them; forged values sit further left.
	if got := l.ClientIP(req("10.9.9.9:443", "9.9.9.9, 1.2.3.4, 10.0.0.2, 10.0.0.3")); got != "1.2.3.4" {
		t.Fatalf("multi-hop: want 1.2.3.4, got %q", got)
	}
}

// TestProxiedRotatingLeftmostSharesOneBucket proves the fix collapses two
// requests with different forged left-most XFF values but the same real client
// into ONE bucket, so the per-IP limit still bites behind a trusted proxy.
func TestProxiedRotatingLeftmostSharesOneBucket(t *testing.T) {
	l := New(60, 1, "10.0.0.0/8") // burst 1: same key's 2nd request is denied
	defer l.Stop()
	if !l.Allow(l.ClientIP(req("10.0.0.5:443", "forged-A, 1.2.3.4"))) {
		t.Fatal("first request should pass")
	}
	if l.Allow(l.ClientIP(req("10.0.0.5:443", "forged-B, 1.2.3.4"))) {
		t.Fatal("rotating forged left-most XFF must not mint a fresh bucket for the same real client")
	}
}

// TestClientIP_TrustedButAllHopsTrustedOrGarbage falls back to RemoteAddr when
// the header carries no genuine client (all-trusted or malformed boundary).
func TestClientIP_TrustedButAllHopsTrustedOrGarbage(t *testing.T) {
	l := New(60, 10, "10.0.0.0/8")
	defer l.Stop()
	if got := l.ClientIP(req("10.0.0.5:443", "10.0.0.2, 10.0.0.3")); got != "10.0.0.5" {
		t.Fatalf("all-trusted hops: want RemoteAddr 10.0.0.5, got %q", got)
	}
	if got := l.ClientIP(req("10.0.0.5:443", "not-an-ip, 10.0.0.2")); got != "10.0.0.5" {
		t.Fatalf("malformed boundary: want RemoteAddr 10.0.0.5, got %q", got)
	}
	if got := l.ClientIP(req("10.0.0.5:443", "")); got != "10.0.0.5" {
		t.Fatalf("absent XFF: want RemoteAddr 10.0.0.5, got %q", got)
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
