package fxsource

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/vul-os/openrate/internal/safedial"
)

// The other half of the SSRF story that redirect_test.go opened.
//
// Refusing redirects stops a feed steering this process somewhere else. It pins
// nothing about where the feed's OWN hostname points, so a name that answers a
// public address on the lookup an operator did by hand and 169.254.169.254 on
// the one this process does reached the cloud metadata service on the first
// fetch — and the outcome came back out through Status.LastError, which /readyz
// and /api/v1/meta publish unauthenticated.
//
// The policy lives in internal/safedial and is tested there. What this file
// asserts is the thing a policy in one package cannot assert about thirteen
// adapters in another: that every single client openrate builds actually
// carries it. It walks the registry rather than a hand-written list, so an
// adapter added tomorrow with a bare &http.Client{} fails here.

// clientOf pulls an adapter's *http.Client out by field name.
//
// Reflection, because Source has no accessor for it and adding one to the
// public interface to satisfy a test would be the wrong trade. A source with no
// Client field is a failure and not a skip: it would be an adapter dialling by
// some other route, which is exactly what this test exists to notice.
func clientOf(t *testing.T, name string, s Source) *http.Client {
	t.Helper()
	v := reflect.ValueOf(s)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	f := v.FieldByName("Client")
	if !f.IsValid() {
		t.Fatalf("source %q has no Client field, so this test cannot see how it dials — and neither "+
			"can newClient, which means it does not carry the dial policy either", name)
	}
	c, ok := f.Interface().(*http.Client)
	if !ok || c == nil {
		t.Fatalf("source %q has a Client field of type %s, not a non-nil *http.Client", name, f.Type())
	}
	return c
}

// TestEverySourceClientRefusesAPrivateDestination drives all thirteen adapters'
// real clients at one server, reached two ways: by the 127.0.0.1 literal an
// operator would have written down, and by a NAME that resolves to it. The
// literal must be served and the name must be refused.
func TestEverySourceClientRefusesAPrivateDestination(t *testing.T) {
	// The premise. A machine that cannot resolve localhost has no stand-in for
	// a name that points into private space, and this test would be measuring
	// nothing — so it fails rather than skips.
	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), "localhost")
	if err != nil || len(ips) == 0 {
		t.Fatalf("this machine cannot resolve localhost (%v); there is no rebinding stand-in", err)
	}

	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"secret":"internal metadata"}`))
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse %q: %v", srv.URL, err)
	}
	named := (&url.URL{Scheme: u.Scheme, Host: net.JoinHostPort("localhost", u.Port())}).String()

	names := make([]string, 0, len(constructors))
	for name := range constructors {
		names = append(names, name)
	}
	sort.Strings(names)

	checked := 0
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			client := clientOf(t, name, constructors[name]())

			// Control first: this client CAN reach that server when the address
			// is the operator's own literal. Without it, "refused" below would
			// be satisfied by a client that cannot connect to anything.
			before := hits.Load()
			resp, err := client.Get(srv.URL) //nolint:noctx // a test against its own httptest server
			if err != nil {
				t.Fatalf("%s cannot reach %s at all: %v — the refusal below would prove nothing",
					name, srv.URL, err)
			}
			resp.Body.Close()
			if hits.Load() != before+1 {
				t.Fatalf("%s's control request never arrived (%d -> %d)", name, before, hits.Load())
			}

			before = hits.Load()
			resp, err = client.Get(named) //nolint:noctx // a test against its own httptest server
			if err == nil {
				resp.Body.Close()
				t.Fatalf("%s reached %s. Its client does not carry the dial policy, so a feed hostname "+
					"that rebinds to 169.254.169.254 reaches the metadata service through it", name, named)
			}
			if !errors.Is(err, safedial.ErrPrivateDestination) {
				t.Fatalf("%s failed against %s with %v, which is not the refusal — something else "+
					"stopped it, and the guard is unproven for this adapter", name, named, err)
			}
			if got := hits.Load(); got != before {
				t.Errorf("%s reached the server %d time(s) despite the refusal", name, got-before)
			}
		})
		checked++
	}

	// Coverage floor. A registry that lost entries, or a t.Run that stopped
	// running, must not report a clean sweep.
	if checked != len(constructors) || checked != 13 {
		t.Fatalf("checked %d of %d source clients; openrate ships 13", checked, len(constructors))
	}
}
