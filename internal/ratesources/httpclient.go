package ratesources

import (
	"net/http"
	"time"
)

// noRedirect is the CheckRedirect policy every outbound source client uses.
//
// Go's default policy follows up to ten redirects to any host, so a feed that is
// compromised, hijacked at DNS, or misconfigured can point an openrate process
// at anything it can reach — a cloud metadata service on 169.254.169.254, an
// internal admin port, a neighbouring container. The failure text is not private
// either: it reaches the per-source status that /readyz and /api/v1/meta publish
// unauthenticated, which turns a followed redirect into a blind-SSRF oracle.
//
// None of the sources here needs redirects: every configured endpoint answers
// directly (verified against the live hosts), so the policy is a flat refusal.
//
// Returning http.ErrUseLastResponse hands the 3xx back unchanged; the caller's
// existing status check rejects it and the resulting error reads "<source>:
// status 302", recording that a redirect was attempted without echoing the
// attacker-chosen Location into a publicly readable field — which returning an
// error from CheckRedirect would do, via the *url.Error wrapper.
//
// This mirrors fxsource's identical policy rather than importing it: a
// three-line hook is not worth coupling the interest-rate sources to the FX
// package, or widening fxsource's public API to share it.
func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

// newClient builds the standard outbound client for a source adapter: a hard
// overall timeout and a refusal to follow redirects.
func newClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: noRedirect}
}

// newClientWithTransport is newClient for adapters that need a tuned transport
// (the SARB host is slow enough to need a bounded dial).
func newClientWithTransport(timeout time.Duration, rt http.RoundTripper) *http.Client {
	return &http.Client{Timeout: timeout, Transport: rt, CheckRedirect: noRedirect}
}
