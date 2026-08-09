package fxsource

import (
	"fmt"
	"strings"
	"testing"
)

// junkCodes builds n synthetic three-to-four letter codes that are not in the
// allowlist. A feed under an attacker's control is not limited to plausible
// currencies: every distinct code becomes a node in the rate graph and the
// cross-rate search is quadratic in the node count, so an unfiltered ingest is a
// memory-exhaustion vector, not just noise in the table.
func junkCodes(n int) []string {
	out := make([]string, 0, n)
	for i := 0; len(out) < n; i++ {
		c := fmt.Sprintf("Q%03d", i)
		if !allowed(c) {
			out = append(out, c)
		}
	}
	return out
}

// TestECBRejectsCodesOutsideTheAllowlist feeds ECB's parser a document with two
// real currencies and 500 invented ones, all with well-formed positive rates so
// that nothing but the allowlist can drop them.
func TestECBRejectsCodesOutsideTheAllowlist(t *testing.T) {
	junk := junkCodes(500)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><gesmes:Envelope xmlns:gesmes="x"><Cube><Cube time="2026-06-30">`)
	b.WriteString(`<Cube currency="USD" rate="1.08"/><Cube currency="ZAR" rate="19.5"/>`)
	for _, c := range junk {
		fmt.Fprintf(&b, `<Cube currency="%s" rate="1.5"/>`, c)
	}
	b.WriteString(`</Cube></Cube></gesmes:Envelope>`)

	srv := serve(t, 200, b.String())
	e := NewECB()
	e.URL = srv.URL
	edges, err := e.Fetch(ctx())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	// Coverage assertion: the two real currencies must survive, so an
	// over-aggressive filter (or an empty parse) cannot pass this vacuously.
	if len(edges) != 2 {
		t.Fatalf("want exactly the 2 allowlisted currencies, got %d edges", len(edges))
	}
	got := map[string]bool{}
	for _, ed := range edges {
		got[ed.To] = true
	}
	if !got["USD"] || !got["ZAR"] {
		t.Fatalf("allowlisted currencies were dropped: %v", got)
	}
	for _, c := range junk {
		if got[c] {
			t.Fatalf("ecb admitted the non-allowlisted code %q; %d such codes were offered", c, len(junk))
		}
	}
	t.Logf("ecb kept 2 allowlisted codes and dropped all %d injected ones", len(junk))
}

// TestFrankfurterRejectsCodesOutsideTheAllowlist is the same test for the JSON
// mirror of the same data.
func TestFrankfurterRejectsCodesOutsideTheAllowlist(t *testing.T) {
	junk := junkCodes(500)
	var b strings.Builder
	b.WriteString(`{"base":"EUR","date":"2026-06-30","rates":{"USD":1.08,"ZAR":19.5`)
	for _, c := range junk {
		fmt.Fprintf(&b, `,"%s":1.5`, c)
	}
	b.WriteString(`}}`)

	srv := serve(t, 200, b.String())
	f := NewFrankfurter()
	f.URL = srv.URL
	edges, err := f.Fetch(ctx())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("want exactly the 2 allowlisted currencies, got %d edges", len(edges))
	}
	got := map[string]bool{}
	for _, ed := range edges {
		if ed.From != "EUR" {
			t.Fatalf("frankfurter edge is not EUR-based: %+v", ed)
		}
		got[ed.To] = true
	}
	if !got["USD"] || !got["ZAR"] {
		t.Fatalf("allowlisted currencies were dropped: %v", got)
	}
	for _, c := range junk {
		if got[c] {
			t.Fatalf("frankfurter admitted the non-allowlisted code %q; %d such codes were offered", c, len(junk))
		}
	}
	t.Logf("frankfurter kept 2 allowlisted codes and dropped all %d injected ones", len(junk))
}

// TestAllowlistCoversTheECBDailyFile guards the other direction: the filter was
// added on the claim that it drops nothing ECB actually publishes. If a future
// edit narrows fiatAllow, a real reference rate would vanish from the graph with
// no error anywhere, so the claim is asserted rather than trusted.
func TestAllowlistCoversTheECBDailyFile(t *testing.T) {
	// The currencies quoted in eurofxref-daily.xml as of this change.
	published := []string{
		"USD", "JPY", "BGN", "CZK", "DKK", "GBP", "HUF", "PLN", "RON", "SEK",
		"CHF", "ISK", "NOK", "TRY", "AUD", "BRL", "CAD", "CNY", "HKD", "IDR",
		"ILS", "INR", "KRW", "MXN", "MYR", "NZD", "PHP", "SGD", "THB", "ZAR",
	}
	for _, c := range published {
		if !allowed(c) {
			t.Errorf("ECB publishes %q but the allowlist drops it: the ecb and frankfurter "+
				"sources would silently lose that currency", c)
		}
	}
	t.Logf("all %d ECB-published currencies are allowlisted", len(published))
}
