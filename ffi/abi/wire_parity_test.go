package abi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/vul-os/openrate/fxsource"
	"github.com/vul-os/openrate/serve"
)

// The ABI's response shapes are declared in engine.go, duplicated from
// serve/api.go rather than imported. The duplication is forced: importing serve
// would pull the HTTP server and the embedded web console into every shared
// library openrate ships, which is the cost the Engine/Refresher split exists to
// avoid.
//
// Duplicating a wire shape is how a mock drifts from its core, and that drift is
// silent by construction — both sides stay internally consistent and both sides
// keep passing their own tests. This file is the thing that makes it loud: one
// engine, one snapshot, one pinned clock, the same question asked over the ABI
// and over serve's real handler, and the two documents required to be equal.
//
// If serve gains a field, renames one, or changes how it ages a leg, this fails.

// pinnedNow is the instant every response in this file is built at. Ageing is
// the one part of these documents that moves on its own, so it is nailed down
// rather than tolerated.
var pinnedNow = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

// testEdges is a deliberately awkward little book: a direct pair, a pair that
// can only be reached by triangulating, two sources quoting the same pair (so
// the "quotes" array is non-empty and corroboration is assessed), and quote
// times that differ (so leg ages differ from each other and from as_of).
const testEdges = `{
  "built_at": "2026-08-09T12:00:00Z",
  "edges": [
    {"from":"USD","to":"ZAR","rate":18.5,"source":"alpha","time":"2026-08-09T11:00:00Z"},
    {"from":"USD","to":"ZAR","rate":18.52,"source":"beta","time":"2026-08-09T11:30:00Z"},
    {"from":"EUR","to":"USD","rate":1.09,"source":"alpha","time":"2026-08-09T10:00:00Z"},
    {"from":"GBP","to":"EUR","rate":1.17,"source":"beta","time":"2026-08-09T09:15:00Z"}
  ]
}`

// parityFixture builds one engine, loads testEdges into it through the ABI, and
// stands serve's real handler over the same engine with the same pinned clock.
func parityFixture(t *testing.T) (h uint64, srv *httptest.Server) {
	t.Helper()
	h = mustNew(t, `{"base":"ZAR","quiet":true}`)
	eo := engineFor(t, h)
	eo.now = func() time.Time { return pinnedNow }

	if _, err := Call(h, "load", testEdges); err != nil {
		t.Fatalf("load: %v", err)
	}

	api := serve.New(eo.e, serve.Options{
		Now:    func() time.Time { return pinnedNow },
		Status: func() []fxsource.Status { return eo.statuses() },
	})
	srv = httptest.NewServer(api.Handler())
	t.Cleanup(func() {
		srv.Close()
		_ = api.Close()
		Close(h)
	})
	return h, srv
}

func TestWireParityWithTheHTTPAPI(t *testing.T) {
	h, srv := parityFixture(t)

	cases := []struct {
		name    string
		method  string
		request string
		path    string
	}{
		{"convert, explicit amount", "convert", `{"from":"USD","to":"ZAR","amount":100}`,
			"/api/v1/convert?from=USD&to=ZAR&amount=100"},
		{"convert, triangulated", "convert", `{"from":"GBP","to":"ZAR","amount":2.5}`,
			"/api/v1/convert?from=GBP&to=ZAR&amount=2.5"},
		{"convert, defaults", "convert", `{}`,
			"/api/v1/convert"},
		{"convert, lower case and spaces", "convert", `{"from":"  usd ","to":"zar","amount":1}`,
			"/api/v1/convert?from=++usd+&to=zar&amount=1"},
		{"rates, default base", "rates", `{}`,
			"/api/v1/rates"},
		{"rates, explicit base", "rates", `{"base":"EUR"}`,
			"/api/v1/rates?base=EUR"},
		{"meta", "meta", `{}`,
			"/api/v1/meta"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			viaABI, err := Call(h, c.method, c.request)
			if err != nil {
				t.Fatalf("ABI %s%s: %v", c.method, c.request, err)
			}
			viaHTTP := get(t, srv.URL+c.path)
			assertSameJSON(t, viaABI, viaHTTP)
		})
	}

	// Coverage floor. A table that lost its cases would report a clean run.
	if len(cases) < 7 {
		t.Fatalf("only %d parity cases; this check is not covering the API it claims to", len(cases))
	}
}

// TestWireParityCheckCanFail is the control for the file.
//
// assertSameJSON comparing two documents that are equal proves nothing unless
// the comparison can tell two documents apart. Every difference below is one a
// real drift would produce: a renamed field, a missing field, an extra field,
// and a value that differs in the last decimal place — the last being the one a
// sloppy comparison (string prefix, length, key-set only) would let through.
func TestWireParityCheckCanFail(t *testing.T) {
	h, srv := parityFixture(t)
	viaABI, err := Call(h, "convert", `{"from":"USD","to":"ZAR","amount":100}`)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	viaHTTP := get(t, srv.URL+"/api/v1/convert?from=USD&to=ZAR&amount=100")

	if !sameJSON(t, viaABI, viaHTTP) {
		t.Fatalf("the two documents differ before any mutation:\nABI:  %s\nHTTP: %s", viaABI, viaHTTP)
	}

	mutations := []struct {
		name string
		doc  func(map[string]any)
	}{
		{"a renamed field", func(m map[string]any) { m["value"] = m["result"]; delete(m, "result") }},
		{"a missing field", func(m map[string]any) { delete(m, "amount") }},
		{"an extra field", func(m map[string]any) { m["currency"] = "ZAR" }},
		{"a value off in the last place", func(m map[string]any) {
			m["result"] = m["result"].(float64) * (1 + 1e-15)
		}},
		{"a nested field changed", func(m map[string]any) {
			m["rate"].(map[string]any)["hops"] = 99
		}},
	}
	for _, mut := range mutations {
		t.Run(mut.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal([]byte(viaABI), &doc); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			mut.doc(doc)
			broken, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if sameJSON(t, string(broken), viaHTTP) {
				t.Fatalf("the parity check reports %s as identical to the HTTP response. "+
					"It cannot see a difference, so every pass it has ever printed was vacuous.\n"+
					"mutated: %s", mut.name, broken)
			}
		})
	}
}

// TestMetaReportsRefresherStatusLikeTheHTTPAPI covers the one field of /meta
// that an engine cannot fill on its own. It is checked separately because the
// fixture above has no refresher, so "sources" is [] on both sides there —
// equal, but equally empty, which would not notice if the ABI never looked.
func TestMetaReportsRefresherStatusLikeTheHTTPAPI(t *testing.T) {
	h, srv := parityFixture(t)

	// Constructing this fetches nothing; it exists so /meta has sources to
	// report. See TestZeroPacketsThroughTheABI, which counts.
	rh, err := NewRefresher(h, `{"sources":"ecb,coinbase","quiet":true}`)
	if err != nil {
		t.Fatalf("refresher: %v", err)
	}
	defer Close(rh)

	viaABI, err := Call(h, "meta", `{}`)
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	viaHTTP := get(t, srv.URL+"/api/v1/meta")
	assertSameJSON(t, viaABI, viaHTTP)

	// And it is genuinely populated on both sides, not equal-and-empty.
	var doc struct {
		Sources []fxsource.Status `json:"sources"`
	}
	if err := json.Unmarshal([]byte(viaABI), &doc); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if len(doc.Sources) != 2 || doc.Sources[0].Name != "ecb" || doc.Sources[1].Name != "coinbase" {
		t.Fatalf("meta reported sources %+v; want ecb then coinbase, in configuration order", doc.Sources)
	}

	// The refresher's own "status" method must say the same thing as the
	// engine's /meta does, or a host has two disagreeing views of one fact.
	st, err := Call(rh, "status", `{}`)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var fromStatus struct {
		Sources []fxsource.Status `json:"sources"`
	}
	if err := json.Unmarshal([]byte(st), &fromStatus); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if !reflect.DeepEqual(fromStatus.Sources, doc.Sources) {
		t.Errorf("refresher status %+v disagrees with the engine's meta %+v", fromStatus.Sources, doc.Sources)
	}
}

// --- helpers ---------------------------------------------------------------

func get(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx // a test against its own httptest server
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d: %s", url, resp.StatusCode, body)
	}
	return string(body)
}

// sameJSON compares two documents by value, not by bytes: the HTTP API indents
// for a human reading curl output and the ABI does not, and a map's key order
// is not part of either contract.
func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	return reflect.DeepEqual(decodeAny(t, a), decodeAny(t, b))
}

func assertSameJSON(t *testing.T, viaABI, viaHTTP string) {
	t.Helper()
	if !sameJSON(t, viaABI, viaHTTP) {
		t.Errorf("the ABI and the HTTP API describe the same rate differently.\nABI:  %s\nHTTP: %s",
			compact(t, viaABI), compact(t, viaHTTP))
	}
}

func decodeAny(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	return v
}

func compact(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(decodeAny(t, s))
	if err != nil {
		return s
	}
	return string(b)
}
