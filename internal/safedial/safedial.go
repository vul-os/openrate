// Package safedial is openrate's outbound-destination policy: the one place
// that decides which addresses this library's feed clients are allowed to
// connect to.
//
// # The hole it closes
//
// Refusing redirects (see fxsource's noRedirect) stops a feed from STEERING an
// openrate process somewhere else, but it pins nothing about where the feed's
// own hostname points. A name that answers a public address on the lookup an
// operator did by hand and 169.254.169.254 on the one this process does reaches
// the cloud metadata service on the first fetch — and the outcome comes back
// out through fxsource.Status.LastError, which /readyz and /api/v1/meta publish
// unauthenticated. That is DNS rebinding, and no host allow-list fixes it,
// because the name in the allow-list is the name that rebinds.
//
// # The policy
//
// A hostname openrate RESOLVES must land on a public address. An IP literal in
// the configured URL is the operator's own decision and is dialled as written.
//
// Splitting it that way is what makes the check both complete and quiet. An
// attacker who can rebind DNS cannot make openrate reach private space; an
// operator who typed 127.0.0.1 or 10.4.2.9 into a URL named an exact address,
// and an exact address cannot rebind — there is no second lookup to differ from
// the first. It also leaves every test in this repository that points an
// adapter at an httptest server working unchanged, because httptest serves on
// 127.0.0.1 as a literal.
//
// An operator whose private source is reached by NAME sets
// OPENRATE_ALLOW_PRIVATE_SOURCES=1. That is the only way past this, it has to
// be set deliberately, and the default is safe.
//
// # Why the check is a Dialer.Control and not a lookup of our own
//
// Control runs after the address is resolved and before connect, on the
// address the socket will actually use, so there is no window between checking
// and connecting. Resolving separately and dialling the result would work too,
// but it would replace Go's dual-stack dialling (address ordering, Happy
// Eyeballs, per-address fallback) with a hand-rolled loop, for no gain.
//
// The one thing Control costs is the error: the dialer wraps whatever it
// returns in a *net.OpError carrying the address it refused, and that address
// would be published in LastError — the operator's internal IP, or an
// attacker's confirmation that their rebind landed. So the wrapper unwraps it
// back to the bare sentinel, which names nothing.
package safedial

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// AllowPrivateEnv is the operator's escape hatch, for a source that genuinely
// lives on a private network and is reached by name. Any value other than the
// empty string, "0", "false" or "no" turns the check off.
const AllowPrivateEnv = "OPENRATE_ALLOW_PRIVATE_SOURCES"

// ErrPrivateDestination is the refusal, and it deliberately names no address.
// It reaches fxsource.Status.LastError, which /readyz and /api/v1/meta publish
// without authentication, so echoing the resolved IP would leak either the
// operator's internal addressing or a confirmation to whoever chose it.
var ErrPrivateDestination = errors.New(
	"refusing to connect: this source's hostname resolved to a non-public address " +
		"(loopback, link-local or private). Set " + AllowPrivateEnv + "=1 if the source really is " +
		"on a private network")

// AllowPrivate reports whether the operator has opted out.
//
// It is read at dial time rather than cached at construction, so the value a
// process runs with is the value it was given — and so a test can set it around
// one call without a package-level knob that another test could leave flipped.
func AllowPrivate() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(AllowPrivateEnv))) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

// Blocked reports whether ip is an address openrate must not reach by name.
//
// The link-local case is the one this exists for — 169.254.169.254 is the cloud
// metadata service on AWS, GCP, Azure, DigitalOcean and Hetzner alike — but
// loopback (an admin port on this host) and private space (a neighbouring
// container, an internal API) are the same class of target and are refused with
// it. Carrier-grade NAT is included because 100.64.0.0/10 is routable-looking
// and is where several providers put internal services.
//
// A nil IP is blocked: an address the policy cannot classify is not one it can
// vouch for.
func Blocked(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Normalise ::ffff:10.0.0.1 to 10.0.0.1 so the IPv4 predicates see it.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	switch {
	case ip.IsUnspecified(), // 0.0.0.0, :: — "this host", by any route
		ip.IsLoopback(),                // 127.0.0.0/8, ::1
		ip.IsLinkLocalUnicast(),        // 169.254.0.0/16, fe80::/10 — the metadata service
		ip.IsLinkLocalMulticast(),      //
		ip.IsInterfaceLocalMulticast(), //
		ip.IsMulticast(),               //
		ip.IsPrivate():                 // 10/8, 172.16/12, 192.168/16, fc00::/7
		return true
	}
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return true // 100.64.0.0/10, carrier-grade NAT
	}
	return false
}

// DialContext wraps d's dial with the policy above. The returned function is
// what an http.Transport's DialContext field wants.
//
// d is copied, so the caller's dialer keeps whatever Control it had and this
// cannot be turned off by mutating it afterwards.
func DialContext(d *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	base := *d
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialer := base
		if guarded(addr) {
			dialer.Control = refusePrivate
		}
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil && errors.Is(err, ErrPrivateDestination) {
			// Drop the *net.OpError, and the address it carries, with it.
			return nil, ErrPrivateDestination
		}
		return conn, err
	}
}

// guarded decides whether this particular dial goes through the check.
//
// Three cases skip it, and each is a case where the check could only produce a
// false refusal:
//
//   - An IP LITERAL. Nothing was resolved, so nothing can rebind; the operator
//     named this address.
//   - The operator OPTED OUT.
//   - A PROXY. When HTTP(S)_PROXY is set, the transport dials the proxy and the
//     feed's name is resolved at the far end — so this check protects nothing
//     here, and would refuse a perfectly ordinary corporate proxy that happens
//     to live on an internal hostname.
func guarded(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// An address the transport built and this cannot parse: check it.
		return true
	}
	return net.ParseIP(host) == nil && !AllowPrivate() && !isConfiguredProxy(host)
}

// refusePrivate is the net.Dialer.Control hook. It sees the resolved address,
// immediately before connect, which is what makes the check free of a window
// between the lookup and the socket.
func refusePrivate(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ErrPrivateDestination
	}
	if Blocked(net.ParseIP(host)) {
		return ErrPrivateDestination
	}
	return nil
}

// proxyEnv is every variable Go's own ProxyFromEnvironment consults, in the
// same order, so this agrees with the transport about what a proxy is.
var proxyEnv = []string{
	"HTTPS_PROXY", "https_proxy",
	"HTTP_PROXY", "http_proxy",
	"ALL_PROXY", "all_proxy",
}

// isConfiguredProxy reports whether host is the host of a proxy this process's
// environment names. The comparison is on the name as configured, because that
// is the name the transport dials.
func isConfiguredProxy(host string) bool {
	if host == "" {
		return false
	}
	for _, key := range proxyEnv {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		if strings.EqualFold(proxyHost(raw), host) {
			return true
		}
	}
	return false
}

// proxyHost pulls the host out of a proxy setting, which may be a full URL or a
// bare host:port — both spellings that ProxyFromEnvironment accepts.
func proxyHost(raw string) string {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Transport is the outbound transport every feed client in openrate uses: the
// process default, with this policy on its dial.
//
// It is DERIVED from http.DefaultTransport rather than built fresh, for two
// reasons. It inherits the proxy, TLS and HTTP/2 configuration a host may have
// set on the default, which a fresh transport would silently drop. And when the
// default has been REPLACED wholesale — which is how every hermetic test in
// this repository and in ffi/abi stubs the network — it is handed back
// untouched, so the stub still sees every request. A host that has taken over
// the process's outbound transport has taken over this decision with it, and
// that is the honest description of what happens.
func Transport() http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	t := base.Clone()
	// The same dialer shape http.DefaultTransport ships with.
	t.DialContext = DialContext(&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second})
	return t
}
