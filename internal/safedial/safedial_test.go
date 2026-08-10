package safedial

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The dial policy, tested at both ends: the classification on its own, and a
// real TCP connection that has to be refused.
//
// The end-to-end half uses "localhost" as its rebinding stand-in, and that is
// not a shortcut — it is the exact shape of the attack. One server, reached two
// ways: by an IP literal, which the operator wrote down and which cannot point
// anywhere else, and by a NAME that resolves to loopback. The policy has to
// allow the first and refuse the second, and no fake resolver is involved in
// showing it.

// loopbackName is the name every machine resolves to 127.0.0.1.
const loopbackName = "localhost"

// requireLoopbackName fails rather than skips if the premise does not hold. A
// machine that cannot resolve localhost cannot run this repository's tests at
// all, and a skip here would silently retire the only end-to-end coverage the
// policy has.
func requireLoopbackName(t *testing.T) {
	t.Helper()
	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), loopbackName)
	if err != nil || len(ips) == 0 {
		t.Fatalf("this machine cannot resolve %q (%v); the rebinding tests below have no stand-in "+
			"for a name that points into private space", loopbackName, err)
	}
	for _, ip := range ips {
		if !ip.IP.IsLoopback() {
			t.Fatalf("%q resolves to %s, which is not loopback; the test premise is broken",
				loopbackName, ip.IP)
		}
	}
}

// byName rewrites an httptest URL to reach the same server through a hostname
// instead of the 127.0.0.1 literal it was born with.
func byName(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	if net.ParseIP(u.Hostname()) == nil {
		t.Fatalf("httptest served %q, which is already a name; this rewrite assumes a literal", raw)
	}
	u.Host = net.JoinHostPort(loopbackName, u.Port())
	return u.String()
}

// trapServer is a server that must not be reached by name, and a counter that
// proves it was not.
func trapServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"secret":"internal metadata"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func guardedClient() *http.Client {
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: Transport(),
	}
}

// TestANameThatResolvesIntoPrivateSpaceIsRefused is the whole point of the
// package. Same server, same port, same bytes behind it — refused when reached
// by name, served when reached by the literal an operator typed.
func TestANameThatResolvesIntoPrivateSpaceIsRefused(t *testing.T) {
	requireLoopbackName(t)
	srv, hits := trapServer(t)
	client := guardedClient()

	// The control comes FIRST, so a failure below cannot be a dead server.
	resp, err := client.Get(srv.URL) //nolint:noctx // a test against its own httptest server
	if err != nil {
		t.Fatalf("the literal %s was refused: %v — an address the operator wrote down is theirs, and "+
			"this control is what makes the refusal below meaningful", srv.URL, err)
	}
	resp.Body.Close()
	if hits.Load() != 1 {
		t.Fatalf("the control request did not reach the server (%d hits); nothing below is measuring "+
			"anything", hits.Load())
	}

	named := byName(t, srv.URL)
	resp, err = client.Get(named) //nolint:noctx // a test against its own httptest server
	if err == nil {
		resp.Body.Close()
		t.Fatalf("GET %s succeeded. A hostname that resolves into loopback is exactly what a rebinding "+
			"feed looks like: public on the lookup an operator did by hand, 169.254.169.254 on the one "+
			"this process does", named)
	}
	if !errors.Is(err, ErrPrivateDestination) {
		t.Errorf("GET %s failed with %v, which is not the refusal — it may have failed for some other "+
			"reason, in which case the guard is not what stopped it", named, err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("the server was reached %d time(s); the named request got through", got-1)
	}

	// The refusal must name no address. It lands in fxsource.Status.LastError,
	// which /readyz and /api/v1/meta publish without authentication.
	for _, leak := range []string{"127.0.0.1", "[::1]", srv.Listener.Addr().String()} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("the refusal published %q: %v — that is the operator's internal addressing, or an "+
				"attacker's confirmation that their rebind landed", leak, err)
		}
	}
}

// TestTheOperatorCanOptOut pins the escape hatch, and that it is off by default.
func TestTheOperatorCanOptOut(t *testing.T) {
	requireLoopbackName(t)
	srv, hits := trapServer(t)
	named := byName(t, srv.URL)

	if _, err := guardedClient().Get(named); err == nil { //nolint:noctx // a test against its own httptest server
		t.Fatalf("GET %s succeeded with %s unset; the default has to be safe", named, AllowPrivateEnv)
	}
	before := hits.Load()

	t.Setenv(AllowPrivateEnv, "1")
	resp, err := guardedClient().Get(named) //nolint:noctx // a test against its own httptest server
	if err != nil {
		t.Fatalf("GET %s with %s=1: %v — an operator whose source really is on a private network has "+
			"no other way past this", named, AllowPrivateEnv, err)
	}
	resp.Body.Close()
	if hits.Load() != before+1 {
		t.Errorf("the opt-out was accepted but the request did not arrive (%d -> %d)", before, hits.Load())
	}
}

// TestOptOutParsing pins which spellings turn the check off. "0" and "false"
// must NOT: an operator who wrote OPENRATE_ALLOW_PRIVATE_SOURCES=0 meant off.
func TestOptOutParsing(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"0":     false,
		"false": false,
		"FALSE": false,
		"no":    false,
		" 0 ":   false,
		"1":     true,
		"true":  true,
		"yes":   true,
		"on":    true,
	}
	for value, want := range cases {
		t.Setenv(AllowPrivateEnv, value)
		if got := AllowPrivate(); got != want {
			t.Errorf("%s=%q -> AllowPrivate() = %v, want %v", AllowPrivateEnv, value, got, want)
		}
	}
	if len(cases) != 10 {
		t.Fatalf("this table pins 10 spellings, found %d", len(cases))
	}
}

// TestBlockedClassification is the address policy on its own, in both
// directions. The "must be reachable" half matters as much as the other: a
// guard that refuses everything is not a guard, it is an outage.
func TestBlockedClassification(t *testing.T) {
	blocked := map[string]string{
		"169.254.169.254":        "the cloud metadata service — the address this whole package exists for",
		"169.254.0.1":            "link-local",
		"fe80::1":                "IPv6 link-local",
		"127.0.0.1":              "loopback",
		"127.1.2.3":              "the rest of 127/8",
		"::1":                    "IPv6 loopback",
		"10.0.0.1":               "RFC 1918",
		"172.16.5.4":             "RFC 1918",
		"172.31.255.255":         "RFC 1918, top of the range",
		"192.168.1.1":            "RFC 1918",
		"fd00::1":                "IPv6 unique-local",
		"0.0.0.0":                "unspecified",
		"::":                     "IPv6 unspecified",
		"100.64.0.1":             "carrier-grade NAT",
		"100.127.255.255":        "carrier-grade NAT, top of the range",
		"224.0.0.1":              "multicast",
		"::ffff:169.254.169.254": "the metadata service spelled as a 4-in-6 address",
		"::ffff:10.0.0.1":        "RFC 1918 spelled as a 4-in-6 address",
	}
	for addr, why := range blocked {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("%s is not parseable; this row tests nothing", addr)
		}
		if !Blocked(ip) {
			t.Errorf("%s (%s) is admitted", addr, why)
		}
	}

	allowed := map[string]string{
		"1.1.1.1":              "an ordinary public address",
		"8.8.8.8":              "an ordinary public address",
		"93.184.216.34":        "an ordinary public address",
		"172.32.0.1":           "just ABOVE RFC 1918's 172.16/12 — the off-by-one a mask gets wrong",
		"172.15.255.255":       "just BELOW RFC 1918's 172.16/12",
		"100.63.255.255":       "just below carrier-grade NAT",
		"100.128.0.0":          "just above carrier-grade NAT",
		"169.253.255.255":      "just below link-local",
		"169.255.0.0":          "just above link-local",
		"2606:4700:4700::1111": "an ordinary public IPv6 address",
	}
	for addr, why := range allowed {
		ip := net.ParseIP(addr)
		if ip == nil {
			t.Fatalf("%s is not parseable; this row tests nothing", addr)
		}
		if Blocked(ip) {
			t.Errorf("%s (%s) is refused; the guard has to leave every real feed reachable", addr, why)
		}
	}

	if Blocked(nil) != true {
		t.Error("a nil IP is admitted; an address the policy cannot classify is not one it can vouch for")
	}
	if len(blocked) != 18 || len(allowed) != 10 {
		t.Fatalf("this test pins 18 blocked and 10 allowed addresses, found %d and %d",
			len(blocked), len(allowed))
	}
}

// TestAProxyOnAPrivateHostnameIsStillDialled covers the one case where applying
// the check would be a pure false refusal: with HTTPS_PROXY set, the transport
// dials the PROXY and the feed's name is resolved at the far end, so nothing
// here is protecting anything — but a corporate proxy on an internal hostname
// would be refused.
func TestAProxyOnAPrivateHostnameIsStillDialled(t *testing.T) {
	requireLoopbackName(t)
	srv, hits := trapServer(t)
	named := byName(t, srv.URL)

	if _, err := guardedClient().Get(named); err == nil { //nolint:noctx // a test against its own httptest server
		t.Fatalf("GET %s succeeded before the proxy was configured; this test would prove nothing", named)
	}

	// Name the same host as the proxy. The dial target is unchanged; only its
	// role is.
	t.Setenv("HTTPS_PROXY", "http://"+loopbackName+":"+portOf(t, srv.URL))
	before := hits.Load()
	resp, err := guardedClient().Get(named) //nolint:noctx // a test against its own httptest server
	if err != nil {
		t.Fatalf("GET %s with the same host named as HTTPS_PROXY: %v — an operator behind a proxy on an "+
			"internal hostname cannot reach anything at all", named, err)
	}
	resp.Body.Close()
	if hits.Load() != before+1 {
		t.Errorf("the proxy exemption was taken but no request arrived (%d -> %d)", before, hits.Load())
	}
}

func portOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u.Port()
}

// TestTransportDefersToAReplacedProcessDefault pins the property every
// hermetic test in this repository and in ffi/abi depends on: a stub installed
// as http.DefaultTransport still sees every request. It is also the honest
// limit of the guard, written down where it can be checked rather than left to
// be discovered.
func TestTransportDefersToAReplacedProcessDefault(t *testing.T) {
	var seen atomic.Int64
	stub := &countingTransport{fn: func(req *http.Request) (*http.Response, error) {
		seen.Add(1)
		return &http.Response{StatusCode: 200, Body: http.NoBody, Request: req}, nil
	}}
	old := http.DefaultTransport
	http.DefaultTransport = stub
	t.Cleanup(func() { http.DefaultTransport = old })

	rt := Transport()
	if rt != http.RoundTripper(stub) {
		t.Fatalf("Transport() built its own transport over a replaced default (%T); every stubbed test "+
			"in this repository would start reaching the real network", rt)
	}
	resp, err := (&http.Client{Transport: rt}).Get("http://example.invalid/") //nolint:noctx // stubbed
	if err != nil {
		t.Fatalf("through the stub: %v", err)
	}
	resp.Body.Close()
	if seen.Load() != 1 {
		t.Errorf("the stub saw %d request(s), want 1", seen.Load())
	}

	// And with the real default back, it does NOT hand the default itself out —
	// it clones and installs the dial policy, or the guard would be absent.
	http.DefaultTransport = old
	if got := Transport(); got == http.DefaultTransport {
		t.Error("Transport() returned http.DefaultTransport itself, so nothing carries the dial policy")
	}
}

// countingTransport is a pointer type on purpose: the assertion above compares
// the value Transport() handed back against the stub by identity, and a func
// type is not comparable.
type countingTransport struct {
	fn func(*http.Request) (*http.Response, error)
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return c.fn(r) }
