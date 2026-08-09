package serve_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/serve"
)

// These tests drive the real HTTP surface over the real Engine — no test double
// stands in for either. An engine is loaded directly with a snapshot, which is
// the point of the library split: populating one no longer needs a refresh
// loop, a goroutine, and a two-second poll waiting for it to finish.

var apiTestTime = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
var testEdges = []fx.Edge{
	{From: "USD", To: "ZAR", Rate: 18.50, Source: "sarb", Time: apiTestTime},
	{From: "USD", To: "EUR", Rate: 0.92, Source: "ecb", Time: apiTestTime},
}

// populatedEngine returns an engine holding exactly these edges. It fails the
// test if the book materialized smaller than expected — a fixture that came out
// empty would make every assertion below vacuous.
func populatedEngine(t *testing.T, edges []fx.Edge, minCurrencies int) *openrate.Engine {
	t.Helper()
	g := fx.NewGraph()
	g.Replace("test", edges)
	snap := g.Materialize(apiTestTime)
	if len(snap.Currencies) < minCurrencies {
		t.Fatalf("fixture materialized %d currencies, want at least %d", len(snap.Currencies), minCurrencies)
	}
	e := openrate.NewEngine(openrate.EngineOptions{Now: func() time.Time { return apiTestTime }})
	e.Load(snap)
	return e
}

// emptyServer is the other half of the fixture: a server over an engine that
// has never been loaded, which is what a caller hits before the first refresh.
func emptyServer(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{}).Routes(mux)
	return mux
}

func apiServer(t *testing.T, e *openrate.Engine) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	serve.New(e, serve.Options{Now: func() time.Time { return apiTestTime }}).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// ─── /healthz ────────────────────────────────────────────────────────────────

func TestHealthzEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{}).Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200", resp.StatusCode)
	}
}

// ─── /api/v1/meta ────────────────────────────────────────────────────────────

func TestMetaEndpointShape(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	resp, err := http.Get(srv.URL + "/api/v1/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/meta status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /meta: %v", err)
	}
	if _, ok := body["currencies"]; !ok {
		t.Error("/meta must include 'currencies' key")
	}
	if _, ok := body["built_at"]; !ok {
		t.Error("/meta must include 'built_at' key")
	}
}

// ─── /api/v1/rates ───────────────────────────────────────────────────────────

func TestRatesEndpointDefaultBase(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	resp, err := http.Get(srv.URL + "/api/v1/rates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/rates status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /rates: %v", err)
	}
	if body["base"] != "ZAR" {
		t.Errorf("base = %v, want ZAR", body["base"])
	}
}

func TestRatesEndpointExplicitBase(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	resp, err := http.Get(srv.URL + "/api/v1/rates?base=USD")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /rates: %v", err)
	}
	if body["base"] != "USD" {
		t.Errorf("base = %v, want USD", body["base"])
	}
	rates, ok := body["rates"].(map[string]any)
	if !ok {
		t.Fatal("rates field must be a map")
	}
	if _, ok := rates["ZAR"]; !ok {
		t.Error("ZAR must appear in rates when base=USD")
	}
	if _, ok := rates["EUR"]; !ok {
		t.Error("EUR must appear in rates when base=USD")
	}
}

// ─── /api/v1/convert ─────────────────────────────────────────────────────────

func TestConvertKnownPair(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	resp, err := http.Get(srv.URL + "/api/v1/convert?from=USD&to=ZAR&amount=100")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/convert status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /convert: %v", err)
	}
	if body["from"] != "USD" || body["to"] != "ZAR" {
		t.Errorf("from/to = %v/%v, want USD/ZAR", body["from"], body["to"])
	}
	result, ok := body["result"].(float64)
	if !ok {
		t.Fatal("result must be a number")
	}
	want := 100.0 * 18.50
	if diff := result - want; diff < -0.01 || diff > 0.01 {
		t.Errorf("result = %v, want ~%v (100 × 18.50)", result, want)
	}
}

func TestConvertDefaultAmountIsOne(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	resp, err := http.Get(srv.URL + "/api/v1/convert?from=USD&to=ZAR")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode /convert: %v", err)
	}
	if body["amount"] != 1.0 {
		t.Errorf("default amount = %v, want 1.0", body["amount"])
	}
}

func TestConvertUnknownPairIs404(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	resp, err := http.Get(srv.URL + "/api/v1/convert?from=XXX&to=YYY")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/convert unknown pair: status = %d, want 404", resp.StatusCode)
	}
}

func TestConvertSelfIsOne(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	resp, err := http.Get(srv.URL + "/api/v1/convert?from=USD&to=USD&amount=42")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/convert self status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["result"] != 42.0 {
		t.Errorf("USD->USD 42 result = %v, want 42", body["result"])
	}
}

func TestConvertMalformedAmountFallsBackToOne(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	resp, err := http.Get(srv.URL + "/api/v1/convert?from=USD&to=ZAR&amount=notanumber")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/convert malformed amount: status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Malformed amount defaults to 1.0.
	if body["amount"] != 1.0 {
		t.Errorf("malformed amount: amount = %v, want 1.0 (default)", body["amount"])
	}
}

// TestConvertNonFiniteAmountIs400 guards the fix for a robustness bug: Inf/NaN
// parse successfully via ParseFloat but poison the multiplication and make the
// JSON encoder fail mid-write, leaving the client a 200 with a truncated body.
// They must be rejected cleanly with a 400 instead.
func TestConvertNonFiniteAmountIs400(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	for _, amt := range []string{"Inf", "-Inf", "NaN", "inf", "nan", "Infinity"} {
		resp, err := http.Get(srv.URL + "/api/v1/convert?from=USD&to=ZAR&amount=" + url.QueryEscape(amt))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("amount=%q: status = %d, want 400 (body: %s)", amt, resp.StatusCode, body)
		}
	}
}

// ─── CORS / JSON content-type ─────────────────────────────────────────────────

// TestSuccessfulEndpointsSetCORSAndJSON verifies that endpoints which always
// produce a JSON body (meta, rates) set the correct CORS and Content-Type
// headers. The convert endpoint is excluded here because an unknown pair takes
// the http.Error path which intentionally omits CORS headers.
func TestSuccessfulEndpointsSetCORSAndJSON(t *testing.T) {
	mux := http.NewServeMux()
	serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{}).Routes(mux)

	for _, path := range []string{"/api/v1/meta", "/api/v1/rates"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, r)
		if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("%s: ACAO = %q, want *", path, got)
		}
		if got := rr.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("%s: Content-Type = %q, want application/json", path, got)
		}
	}
}
