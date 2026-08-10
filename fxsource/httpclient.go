package fxsource

import (
	"net/http"
	"time"

	"github.com/vul-os/openrate/internal/safedial"
)

// noRedirect is the CheckRedirect policy every outbound feed client uses.
//
// Go's default policy follows up to ten redirects to any host. A feed that is
// compromised, hijacked at DNS, or simply misconfigured can therefore point an
// openrate process at anything the process can reach — a cloud metadata service
// on 169.254.169.254, an internal admin port, a neighbouring container. The
// resulting error text is not private either: it lands in fxsource.Status
// LastError, which /readyz and /api/v1/meta publish unauthenticated, so an
// attacker who can steer a redirect gets a blind-SSRF oracle back out of the
// public API.
//
// None of the feeds openrate ships needs redirects: every configured endpoint
// answers 200 directly (verified against each live host). So the policy is a
// flat refusal, with no per-source exception and no host allow-list to keep
// correct.
//
// Returning http.ErrUseLastResponse rather than an error is deliberate. The
// client hands back the 3xx response unchanged, each adapter's existing status
// check rejects it, and the error the adapter produces reads "<source>: status
// 302" — it records that the feed tried to redirect without echoing the
// attacker-chosen Location back into a publicly readable field. An error
// returned from CheckRedirect would instead be wrapped in a *url.Error carrying
// the redirect target, which is the leak this policy exists to prevent.
func noRedirect(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

// newClient builds the standard outbound client for a feed adapter: a hard
// overall timeout, a refusal to follow redirects, and openrate's dial policy.
//
// The transport is where the second half of the SSRF story lives. noRedirect
// stops a feed steering this process elsewhere; it pins nothing about where the
// feed's OWN hostname points, so a name that resolves publicly once and to
// 169.254.169.254 the next time reached the metadata service. safedial.Transport
// refuses a resolved private, loopback or link-local destination — see that
// package for the policy and for the operator's opt-out. Every adapter here
// gets it because every adapter is built through this function, and
// TestEverySourceClientRefusesAPrivateDestination counts them.
func newClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: safedial.Transport(), CheckRedirect: noRedirect}
}

// newClientWithTransport is newClient for the adapters that need a tuned
// transport (SARB's slow host needs a bounded dial). The caller owns that
// transport, so the caller applies the dial policy to it — safedial.DialContext
// wrapping its net.Dialer, exactly as safedial.Transport does for the default.
func newClientWithTransport(timeout time.Duration, rt http.RoundTripper) *http.Client {
	return &http.Client{Timeout: timeout, Transport: rt, CheckRedirect: noRedirect}
}
