package fxsource

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// trap returns a server that must never be reached, plus its hit counter. It is
// the stand-in for 169.254.169.254: if an adapter follows a feed's redirect, the
// counter is the proof.
func trap(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"secret":"internal metadata"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// redirector returns a server that 302s every request to to.
func redirector(t *testing.T, to string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, to, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestFeedRedirectIsRefused is the SSRF regression. Every shipped adapter used
// Go's default redirect policy, which follows up to ten hops to any host — so a
// feed that is compromised, DNS-hijacked, or merely misconfigured could steer an
// openrate process at a link-local metadata endpoint, and the outcome came back
// out through the unauthenticated /readyz and /api/v1/meta status.
//
// Each case points a real adapter at a server that redirects to a trap. The
// adapter must fail, the trap must never be contacted, and the error must not
// name the redirect target — the error text is published, so echoing an
// attacker-chosen Location would hand the oracle back.
func TestFeedRedirectIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		fetch func(url string) error
	}{
		{"ecb", func(u string) error { s := NewECB(); s.URL = u; _, err := s.Fetch(ctx()); return err }},
		{"coinbase", func(u string) error { s := NewCoinbase(); s.URL = u; _, err := s.Fetch(ctx()); return err }},
		{"luno", func(u string) error { s := NewLuno(); s.URL = u; _, err := s.Fetch(ctx()); return err }},
		{"sarb", func(u string) error { s := NewSARB(); s.URL = u; _, err := s.Fetch(ctx()); return err }},
		{"frankfurter", func(u string) error { s := NewFrankfurter(); s.URL = u; _, err := s.Fetch(ctx()); return err }},
		{"erapi", func(u string) error { s := NewERAPI(); s.URL = u; _, err := s.Fetch(ctx()); return err }},
		{"boc", func(u string) error { s := NewBoC(); s.URL = u; _, err := s.Fetch(ctx()); return err }},
		{"fawazahmed0", func(u string) error {
			s := NewFawaz()
			s.URL, s.Fallback = u, u // both legs must refuse, not just the first
			_, err := s.Fetch(ctx())
			return err
		}},
		{"yahoo", func(u string) error {
			s := NewYahoo()
			s.BaseURL = u + "/chart/"
			_, err := s.Fetch(ctx())
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			internal, hits := trap(t)
			feed := redirector(t, internal.URL)

			err := c.fetch(feed.URL)
			if err == nil {
				t.Fatalf("%s followed or accepted a redirect: want an error", c.name)
			}
			if got := hits.Load(); got != 0 {
				t.Fatalf("%s followed the redirect: the internal host was contacted %d time(s)", c.name, got)
			}
			// The error is published unauthenticated; it must not carry the target.
			host := strings.TrimPrefix(internal.URL, "http://")
			if strings.Contains(err.Error(), host) {
				t.Fatalf("%s error echoes the redirect target %q: %v", c.name, host, err)
			}
		})
	}
}

// TestEveryConstructedSourceRefusesRedirects is the coverage half: the
// behavioural test above cannot exercise the keyed adapters' real endpoints, and
// a source added later would inherit the default policy silently. Every source
// in the registry must hand back a client that refuses redirects.
func TestEveryConstructedSourceRefusesRedirects(t *testing.T) {
	if len(constructors) == 0 {
		t.Fatal("the source registry is empty; this test would verify nothing")
	}
	checked := 0
	for name, mk := range constructors {
		src := mk()
		f := reflect.ValueOf(src).Elem().FieldByName("Client")
		if !f.IsValid() || f.IsNil() {
			t.Errorf("source %q has no *http.Client field to check", name)
			continue
		}
		cl, ok := f.Interface().(*http.Client)
		if !ok {
			t.Errorf("source %q field Client is %s, not *http.Client", name, f.Type())
			continue
		}
		checked++
		if cl.CheckRedirect == nil {
			t.Errorf("source %q builds a client with no CheckRedirect: it follows up to 10 redirects "+
				"to any host, including link-local metadata. Build it with newClient.", name)
			continue
		}
		req, _ := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest/meta-data/", nil)
		if err := cl.CheckRedirect(req, []*http.Request{req}); err != http.ErrUseLastResponse {
			t.Errorf("source %q CheckRedirect = %v, want http.ErrUseLastResponse", name, err)
		}
	}
	if checked != len(constructors) {
		t.Fatalf("checked %d of %d registered sources", checked, len(constructors))
	}
	t.Logf("verified a redirect refusal on all %d registered sources", checked)
}
