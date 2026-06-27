package redact

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestQuery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"apiKey", "https://api.polygon.io/x?apiKey=SECRET", "https://api.polygon.io/x?apiKey=REDACTED"},
		{"api_key", "https://marketdata.tradermade.com/v1/live?currency=EURUSD&api_key=SECRET", "https://marketdata.tradermade.com/v1/live?currency=EURUSD&api_key=REDACTED"},
		{"token", "https://h/p?token=abc123&foo=bar", "https://h/p?token=REDACTED&foo=bar"},
		{"case-insensitive", "https://h/p?ApiKey=abc", "https://h/p?ApiKey=REDACTED"},
		{"no-secret", "https://h/p?currency=EURUSD&base=ZAR", "https://h/p?currency=EURUSD&base=ZAR"},
		{"url.Error shape", `Get "https://h/p?apiKey=SECRET": dial tcp: i/o timeout`, `Get "https://h/p?apiKey=REDACTED": dial tcp: i/o timeout`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Query(c.in); got != c.want {
				t.Errorf("Query(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestError(t *testing.T) {
	if Error(nil) != nil {
		t.Fatal("Error(nil) must be nil")
	}
	err := Error(errors.New(`Get "https://h/p?apiKey=SECRET": timeout`))
	if got := err.Error(); strings.Contains(got, "SECRET") {
		t.Fatalf("redacted error still leaks secret: %q", got)
	}
	if got := err.Error(); !strings.Contains(got, "apiKey=REDACTED") {
		t.Fatalf("expected masked key, got %q", got)
	}
}

// TestErrorFromRealURLError exercises a genuine *url.Error produced by net/http
// to ensure the real-world error shape (the one the audit flagged) is scrubbed.
func TestErrorFromRealURLError(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://127.0.0.1:1/snapshot?apiKey=TOPSECRET", nil)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // closed listener → guaranteed connection error carrying the URL
	_, err := srv.Client().Do(req)
	if err == nil {
		t.Skip("expected a connection error")
	}
	if strings.Contains(Error(err).Error(), "TOPSECRET") {
		t.Fatalf("secret leaked: %q", Error(err).Error())
	}
}
