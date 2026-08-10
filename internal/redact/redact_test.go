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
	// The error comes from port 1 being unreachable, NOT from this close: the
	// request above is aimed at 127.0.0.1:1 and srv is borrowed only for its
	// Client. Closing it is tidiness. The comment used to credit the close,
	// which sent me mutating the wrong line to test this.
	srv.Close()
	_, err := srv.Client().Do(req)
	if err == nil {
		// Not a reason to skip. This test's only assertion is that a real
		// *url.Error carrying a key gets scrubbed; with no error there is
		// nothing to scrub and nothing is verified. Skipping reports success
		// for a run that checked the secret was safe by never looking at it.
		t.Fatal("no connection error, so redaction was never exercised. This test " +
			"needs a guaranteed failure to produce the *url.Error it scrubs — if the " +
			"closed-listener trick has stopped working, fix the setup rather than " +
			"letting the run pass having asserted nothing.")
	}
	if strings.Contains(Error(err).Error(), "TOPSECRET") {
		t.Fatalf("secret leaked: %q", Error(err).Error())
	}
}

// The shapes an exact name-list keeps missing. fxsource.Source is a public
// interface, so the credential spellings this has to survive are not only the
// ones openrate's own adapters use — and Status.LastError, where every one of
// these lands, is published unauthenticated by /readyz and /api/v1/meta.
func TestQueryRedactsCredentialShapesBeyondTheShippedAdapters(t *testing.T) {
	const secret = "SUPERSECRETKEY123"
	cases := []struct {
		name string
		in   string
	}{
		// The shipped adapters (regression: these must never start leaking).
		{"polygon apiKey", "Get \"https://api.polygon.io/v2/x?apiKey=" + secret + "\": dial tcp: i/o timeout"},
		{"tradermade api_key", "Get \"https://marketdata.tradermade.com/api/v1/live?currency=USDZAR&api_key=" + secret + "\": EOF"},
		{"oxr app_id", "Get \"https://openexchangerates.org/api/latest.json?app_id=" + secret + "\": EOF"},
		{"twelvedata apikey", "Get \"https://api.twelvedata.com/exchange_rate?symbol=USD/ZAR&apikey=" + secret + "\": EOF"},
		{"fred api_key", "Get \"https://api.stlouisfed.org/fred/series/observations?api_key=" + secret + "&file_type=json\": EOF"},
		// Shapes no shipped adapter uses yet.
		{"url userinfo", "Get \"https://user:" + secret + "@rates.example.com/v1\": dial tcp: i/o timeout"},
		{"hyphenated access-key", "Get \"https://h/v1?access-key=" + secret + "\": EOF"},
		{"client_secret", "Get \"https://h/v1?client_secret=" + secret + "\": EOF"},
		{"subscription_key", "Get \"https://h/v1?subscription_key=" + secret + "\": EOF"},
		{"x-api-key", "Get \"https://h/v1?x-api-key=" + secret + "\": EOF"},
		{"refresh_token", "Get \"https://h/v1?refresh_token=" + secret + "\": EOF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Query(tc.in)
			if strings.Contains(got, secret) {
				t.Fatalf("credential survived redaction: %q", got)
			}
			if !strings.Contains(got, "REDACTED") {
				t.Fatalf("nothing was redacted, so the match itself is broken: %q", got)
			}
		})
	}
}

// Over-redaction has a cost too: an error that names no source and no reason is
// useless at /readyz. The non-secret parameters the shipped adapters send must
// survive verbatim, and so must the host and path.
func TestQueryKeepsTheDiagnosticParts(t *testing.T) {
	in := "Get \"https://api.stlouisfed.org/fred/series/observations?api_key=SECRET" +
		"&file_type=json&limit=10&series_id=DFF&sort_order=desc\": dial tcp 1.2.3.4:443: connect: connection refused"
	got := Query(in)
	for _, keep := range []string{
		"api.stlouisfed.org", "/fred/series/observations",
		"file_type=json", "limit=10", "series_id=DFF", "sort_order=desc",
		"connection refused",
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("redaction removed a diagnostic part %q from: %s", keep, got)
		}
	}
	if strings.Contains(got, "SECRET") {
		t.Fatalf("credential survived: %s", got)
	}
}
