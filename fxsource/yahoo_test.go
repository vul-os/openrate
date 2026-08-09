package fxsource

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// yahooServer serves the v8 chart shape for any symbol, quoting price and
// counting the symbols it was asked for.
func yahooServer(t *testing.T, price float64) (*httptest.Server, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sym := strings.TrimPrefix(r.URL.Path, "/chart/")
		asked = append(asked, sym)
		fmt.Fprintf(w, `{"chart":{"result":[{"meta":{"regularMarketPrice":%v,"regularMarketTime":1751000000}}],"error":null}}`, price)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

// TestYahooFetch is the test this adapter never had, and it is why the bug
// survived: Fetch's symbol guard demanded len(sym) == 9 while also requiring
// sym[6:] == "=X", which forces len(sym) == 8. No string satisfies both, so
// every symbol was skipped, Fetch always returned "no quotes (rate-limited or
// blocked?)", and the adapter looked throttled rather than broken.
func TestYahooFetch(t *testing.T) {
	srv, asked := yahooServer(t, 18.25)
	y := NewYahoo()
	y.BaseURL = srv.URL + "/chart/"
	y.Symbols = []string{"USDZAR=X", "EURGBP=X"}

	edges, err := y.Fetch(ctx())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("want 2 edges for 2 well-formed symbols, got %d: %+v", len(edges), edges)
	}
	if len(*asked) != 2 {
		t.Fatalf("want 2 upstream requests, got %d (%v)", len(*asked), *asked)
	}
	// The base is read from the symbol, not assumed to be USD: "EURGBP=X" is
	// "1 EUR = price GBP".
	want := map[string]string{"USD": "ZAR", "EUR": "GBP"}
	for _, ed := range edges {
		if want[ed.From] != ed.To {
			t.Fatalf("edge %+v does not match its symbol (want %v)", ed, want)
		}
		if ed.Rate != 18.25 || ed.Source != "yahoo" {
			t.Fatalf("bad edge %+v", ed)
		}
		if ed.Time.IsZero() {
			t.Fatalf("edge %+v has no timestamp", ed)
		}
	}
}

// TestYahooFetchDefaultSymbolsAreUsable pins the shipped configuration: it is
// the default symbol list that the unsatisfiable guard silently rejected, so a
// test on hand-written symbols alone would not have caught it.
func TestYahooFetchDefaultSymbolsAreUsable(t *testing.T) {
	y := NewYahoo()
	if len(y.Symbols) == 0 {
		t.Fatal("NewYahoo configures no symbols; this test would verify nothing")
	}
	srv, asked := yahooServer(t, 2)
	y.BaseURL = srv.URL + "/chart/"

	edges, err := y.Fetch(ctx())
	if err != nil {
		t.Fatalf("Fetch with the default symbols: %v", err)
	}
	if len(edges) != len(y.Symbols) {
		t.Fatalf("want an edge per default symbol (%d), got %d", len(y.Symbols), len(edges))
	}
	if len(*asked) != len(y.Symbols) {
		t.Fatalf("want %d upstream requests, got %d", len(y.Symbols), len(*asked))
	}
}

// TestYahooRejectsMalformedSymbols covers the other half of the parse: junk must
// still be dropped, and — because the misleading error is what hid the bug for
// so long — an all-junk configuration must report the configuration, not an
// upstream throttle.
func TestYahooRejectsMalformedSymbols(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"chart":{"result":[{"meta":{"regularMarketPrice":1}}]}}`)
	}))
	t.Cleanup(srv.Close)

	bad := []string{"USDZARX", "USDZAR=Y", "USZAR=X", "USDZARR=X", "usdzar=X", "", "=X", "USDZAR=X "}
	y := &Yahoo{Symbols: bad, BaseURL: srv.URL + "/chart/", Client: NewYahoo().Client}

	_, err := y.Fetch(ctx())
	if err == nil {
		t.Fatal("want an error when every configured symbol is malformed")
	}
	if hits.Load() != 0 {
		t.Fatalf("malformed symbols reached the network %d time(s)", hits.Load())
	}
	if !strings.Contains(err.Error(), "well-formed FX symbol") {
		t.Fatalf("error should name the configuration fault, got %q", err)
	}
	if strings.Contains(err.Error(), "rate-limited") {
		t.Fatalf("a configuration fault is reported as an upstream throttle: %q", err)
	}

	// A good symbol alongside the junk still works, and only it is fetched.
	y.Symbols = append([]string{"USDZAR=X"}, bad...)
	edges, err := y.Fetch(ctx())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(edges) != 1 || edges[0].From != "USD" || edges[0].To != "ZAR" {
		t.Fatalf("want exactly the one well-formed symbol, got %+v", edges)
	}
	if hits.Load() != 1 {
		t.Fatalf("want 1 upstream request, got %d", hits.Load())
	}
}
