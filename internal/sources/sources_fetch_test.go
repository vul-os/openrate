package sources

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vul-os/openrate/internal/redact"
)

// serve returns an httptest server that replies with the given status and body.
func serve(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func ctx() context.Context { return context.Background() }

func TestECBFetch(t *testing.T) {
	const xml = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
 <Cube>
  <Cube time="2026-06-30">
   <Cube currency="USD" rate="1.0800"/>
   <Cube currency="ZAR" rate="19.5000"/>
   <Cube currency="BAD" rate="not-a-number"/>
   <Cube currency="NEG" rate="-1"/>
   <Cube currency="" rate="1.0"/>
  </Cube>
 </Cube>
</gesmes:Envelope>`
	srv := serve(t, 200, xml)
	e := NewECB()
	e.URL = srv.URL
	edges, err := e.Fetch(ctx())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// Only the two well-formed positive rates should survive.
	if len(edges) != 2 {
		t.Fatalf("want 2 edges, got %d: %+v", len(edges), edges)
	}
	for _, ed := range edges {
		if ed.From != "EUR" || ed.Rate <= 0 || ed.Source != "ecb" {
			t.Fatalf("bad edge %+v", ed)
		}
	}
}

func TestECBFetchStatusAndEmpty(t *testing.T) {
	srv := serve(t, 503, "down")
	e := NewECB()
	e.URL = srv.URL
	if _, err := e.Fetch(ctx()); err == nil {
		t.Fatal("want error on 503")
	}

	// Well-formed XML but no usable rates -> explicit error, not a panic.
	srv2 := serve(t, 200, `<gesmes:Envelope xmlns:gesmes="x"><Cube><Cube time="2026-06-30"></Cube></Cube></gesmes:Envelope>`)
	e2 := NewECB()
	e2.URL = srv2.URL
	if _, err := e2.Fetch(ctx()); err == nil {
		t.Fatal("want error on no rates")
	}
}

func TestCoinbaseFetch(t *testing.T) {
	const body = `{"data":{"currency":"USD","rates":{
		"ZAR":"19.5","EUR":"0.92","DOGE":"0.12","BAD":"x","NEG":"-3"}}}`
	srv := serve(t, 200, body)
	c := NewCoinbase()
	c.URL = srv.URL
	edges, err := c.Fetch(ctx())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// ZAR + EUR are allowlisted; DOGE not; BAD/NEG rejected.
	if len(edges) != 2 {
		t.Fatalf("want 2 edges, got %d: %+v", len(edges), edges)
	}
	for _, ed := range edges {
		if ed.From != "USD" || ed.To == "DOGE" || ed.Rate <= 0 {
			t.Fatalf("bad edge %+v", ed)
		}
	}
}

func TestLunoFetch(t *testing.T) {
	const body = `{"tickers":[
		{"pair":"XBTZAR","last_trade":"1200000","timestamp":1751000000000},
		{"pair":"ETHZAR","last_trade":"60000","timestamp":0},
		{"pair":"XBTUSD","last_trade":"65000","timestamp":1751000000000},
		{"pair":"ZZZZAR","last_trade":"1","timestamp":1751000000000},
		{"pair":"XBTZAR","last_trade":"bad","timestamp":1751000000000}]}`
	srv := serve(t, 200, body)
	l := NewLuno()
	l.URL = srv.URL
	edges, err := l.Fetch(ctx())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// XBT->BTC/ZAR and ETH/ZAR kept; USD-quoted, non-allowlisted base, and bad
	// price dropped.
	if len(edges) != 2 {
		t.Fatalf("want 2 edges, got %d: %+v", len(edges), edges)
	}
	var sawBTC bool
	for _, ed := range edges {
		if ed.To != "ZAR" {
			t.Fatalf("luno edge not ZAR-quoted: %+v", ed)
		}
		if ed.From == "BTC" {
			sawBTC = true
		}
	}
	if !sawBTC {
		t.Fatal("expected XBT normalised to BTC")
	}
}

func TestSARBFetch(t *testing.T) {
	const body = `[
		{"Name":"Rand per US Dollar","TimeseriesCode":"EXCX135D","Date":"2026-06-30","Value":18.2},
		{"Name":"Rand per Euro","TimeseriesCode":"EXCZ002D","Date":"2026-06-30T00:00:00Z","Value":19.7},
		{"Name":"Unknown","TimeseriesCode":"ZZZZ","Date":"2026-06-30","Value":5},
		{"Name":"Bad value","TimeseriesCode":"EXCZ001D","Date":"2026-06-30","Value":-1}]`
	srv := serve(t, 200, body)
	s := NewSARB()
	s.URL = srv.URL
	edges, err := s.Fetch(ctx())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// USD + EUR recognised; unknown code and negative value dropped.
	if len(edges) != 2 {
		t.Fatalf("want 2 edges, got %d: %+v", len(edges), edges)
	}
	for _, ed := range edges {
		if ed.To != "ZAR" || ed.Rate <= 0 {
			t.Fatalf("bad sarb edge %+v", ed)
		}
	}
}

// deadClient returns an http.Client whose every dial fails immediately, so a
// Fetch that reaches the network produces a genuine *url.Error carrying the
// request URL (and thus the API key) — the exact shape the store must redact.
func deadClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DialContext: func(context.Context, string, string) (net.Conn, error) {
				return nil, &net.OpError{Op: "dial", Err: errRefused{}}
			},
		},
	}
}

type errRefused struct{}

func (errRefused) Error() string { return "connection refused" }

// TestPaidSourceKeyNeverLeaks is the secret-leak regression. Each paid source
// embeds its key in the request URL; when the upstream is unreachable, net/http
// returns a *url.Error echoing that URL. We assert (a) the RAW error really does
// contain the key (so the test would catch a redaction regression), and (b)
// redact.Error — the exact call the store makes before logging/exposing — scrubs
// it. This exercises each source's real Fetch, not a hand-written error string.
func TestPaidSourceKeyNeverLeaks(t *testing.T) {
	const secret = "SUPERSECRETKEY123"
	dc := deadClient()

	runs := []struct {
		name string
		run  func() error
	}{
		{"oxr", func() error { _, err := (&OXR{Key: secret, Client: dc}).Fetch(ctx()); return err }},
		{"polygon", func() error { _, err := (&Polygon{Key: secret, Client: dc}).Fetch(ctx()); return err }},
		{"twelvedata", func() error { _, err := (&TwelveData{Key: secret, Client: dc}).Fetch(ctx()); return err }},
		{"tradermade", func() error { _, err := (&TraderMade{Key: secret, Client: dc}).Fetch(ctx()); return err }},
	}
	for _, r := range runs {
		t.Run(r.name, func(t *testing.T) {
			err := r.run()
			if err == nil {
				t.Fatal("expected a network error")
			}
			raw := err.Error()
			if !strings.Contains(raw, secret) {
				t.Fatalf("test premise broken: raw error does not carry the key: %q", raw)
			}
			if red := redact.Error(err).Error(); strings.Contains(red, secret) {
				t.Fatalf("KEY LEAK: redacted error still contains secret: %q", red)
			}
		})
	}
}

// TestPaidSourceMissingKey confirms a paid source with no key fails fast with a
// keyless error and never touches the network.
func TestPaidSourceMissingKey(t *testing.T) {
	for _, run := range []func() error{
		func() error { _, err := (&OXR{Client: deadClient()}).Fetch(ctx()); return err },
		func() error { _, err := (&Polygon{Client: deadClient()}).Fetch(ctx()); return err },
		func() error { _, err := (&TwelveData{Client: deadClient()}).Fetch(ctx()); return err },
		func() error { _, err := (&TraderMade{Client: deadClient()}).Fetch(ctx()); return err },
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "not set") {
			t.Fatalf("want a 'not set' error for missing key, got %v", err)
		}
	}
}
