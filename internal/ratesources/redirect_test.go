package ratesources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

// TestSourceRedirectIsRefused is the SSRF regression for the interest-rate
// sources. Every adapter used Go's default policy, which follows up to ten
// redirects to any host, so a compromised or hijacked feed could steer the
// process at a link-local metadata endpoint — and the resulting error reaches
// the unauthenticated /readyz and /api/v1/meta status, closing the oracle.
//
// The feed server 302s to a trap; the adapter must fail, the trap must never be
// contacted, and the error must not name the redirect target.
func TestSourceRedirectIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		fetch func(url string) error
	}{
		{"bis", func(u string) error { s := NewBIS(); s.URL = u; _, err := s.Fetch(context.Background()); return err }},
		{"sarbrates", func(u string) error {
			s := NewSARBRates()
			s.BaseURL = u
			_, err := s.Fetch(context.Background())
			return err
		}},
		{"fred", func(u string) error {
			s := NewFRED()
			s.Key = "k"
			s.BaseURL = u
			_, err := s.Fetch(context.Background())
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var hits atomic.Int64
			internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = w.Write([]byte("internal metadata"))
			}))
			defer internal.Close()
			feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, internal.URL, http.StatusFound)
			}))
			defer feed.Close()

			err := c.fetch(feed.URL)
			if err == nil {
				t.Fatalf("%s followed or accepted a redirect: want an error", c.name)
			}
			if got := hits.Load(); got != 0 {
				t.Fatalf("%s followed the redirect: the internal host was contacted %d time(s)", c.name, got)
			}
			host := strings.TrimPrefix(internal.URL, "http://")
			if strings.Contains(err.Error(), host) {
				t.Fatalf("%s error echoes the redirect target %q: %v", c.name, host, err)
			}
		})
	}
}

// TestEveryConstructedSourceRefusesRedirects is the coverage half: a source
// added later would silently inherit the default policy.
func TestEveryConstructedSourceRefusesRedirects(t *testing.T) {
	if len(constructors) == 0 {
		t.Fatal("the source registry is empty; this test would verify nothing")
	}
	checked := 0
	for name, mk := range constructors {
		f := reflect.ValueOf(mk()).Elem().FieldByName("Client")
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
