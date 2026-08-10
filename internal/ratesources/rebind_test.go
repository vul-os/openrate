package ratesources

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

// The interest-rate side of fxsource/rebind_test.go, and it exists separately
// for the same reason redirect_test.go does: these adapters build their clients
// through this package's own newClient, so "fxsource is covered" says nothing
// about them. bis and fred go through newClient; sarbrates has its own tuned
// transport and is the one that would be missed by a fix applied in one place.

func clientOf(t *testing.T, name string, s Source) *http.Client {
	t.Helper()
	v := reflect.ValueOf(s)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	f := v.FieldByName("Client")
	if !f.IsValid() {
		t.Fatalf("source %q has no Client field, so it does not dial through newClient and does not "+
			"carry the dial policy", name)
	}
	c, ok := f.Interface().(*http.Client)
	if !ok || c == nil {
		t.Fatalf("source %q has a Client field of type %s, not a non-nil *http.Client", name, f.Type())
	}
	return c
}

// TestEverySourceClientRefusesAPrivateDestination drives each adapter's real
// client at one server reached two ways: by the 127.0.0.1 literal an operator
// would have written down, and by a name that resolves to it.
func TestEverySourceClientRefusesAPrivateDestination(t *testing.T) {
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
				t.Fatalf("%s reached %s. Its client does not carry the dial policy, so a source hostname "+
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

	if checked != len(constructors) || checked != 3 {
		t.Fatalf("checked %d of %d source clients; openrate ships 3 interest-rate sources",
			checked, len(constructors))
	}
}
