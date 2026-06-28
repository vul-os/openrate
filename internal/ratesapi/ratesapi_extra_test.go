package ratesapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vul-os/openrate/internal/rates"
	"github.com/vul-os/openrate/internal/ratestore"
)

// ─── Mock interest-rate source ────────────────────────────────────────────────

// mockRateSource satisfies ratesources.Source structurally.
type mockRateSource struct {
	name string
	obs  []rates.Observation
}

func (m *mockRateSource) Name() string                                         { return m.name }
func (m *mockRateSource) Fetch(_ context.Context) ([]rates.Observation, error) { return m.obs, nil }

var raNow = time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

// testObs are two series across two areas and two types.
var testObs = []rates.Observation{
	{Series: "us.policy", Area: "US", Type: rates.TypePolicy, Value: 5.25, Date: raNow, Name: "US Policy Rate", Source: "fred"},
	{Series: "za.policy", Area: "ZA", Type: rates.TypePolicy, Value: 8.25, Date: raNow, Name: "ZA Policy Rate", Source: "sarbrates"},
	{Series: "za.ref.zaronia", Area: "ZA", Type: rates.TypeReference, Value: 7.95, Date: raNow, Name: "ZARONIA", Source: "sarbrates"},
}

// populatedRateStore creates a ratestore seeded from mockRateSource and blocks
// until the snapshot contains at least minSeries series (or the test fails).
func populatedRateStore(t *testing.T, obs []rates.Observation, minSeries int) (*ratestore.Store, context.CancelFunc) {
	t.Helper()
	ms := &mockRateSource{"test", obs}
	st := ratestore.New(time.Hour, ms)
	ctx, cancel := context.WithCancel(context.Background())
	go st.Run(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(st.Snapshot().IDs()) >= minSeries {
			return st, cancel
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	t.Fatalf("ratestore did not populate with %d series within 2 s", minSeries)
	return nil, nil
}

func ratesAPIServer(t *testing.T, st *ratestore.Store) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	New(st, "*").Routes(mux)
	return httptest.NewServer(mux)
}

// ─── /api/v1/interest/rates ──────────────────────────────────────────────────

func TestInterestRatesEndpointAllSeries(t *testing.T) {
	st, cancel := populatedRateStore(t, testObs, 3)
	defer cancel()
	srv := ratesAPIServer(t, st)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/interest/rates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/interest/rates status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	count, _ := body["count"].(float64)
	if int(count) != 3 {
		t.Errorf("count = %v, want 3", count)
	}
}

func TestInterestRatesFilterByArea(t *testing.T) {
	st, cancel := populatedRateStore(t, testObs, 3)
	defer cancel()
	srv := ratesAPIServer(t, st)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/interest/rates?area=ZA")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// testObs has 2 ZA series and 1 US series; filtering by ZA must return 2.
	count, _ := body["count"].(float64)
	if int(count) != 2 {
		t.Errorf("area=ZA count = %v, want 2", count)
	}
}

func TestInterestRatesFilterByType(t *testing.T) {
	st, cancel := populatedRateStore(t, testObs, 3)
	defer cancel()
	srv := ratesAPIServer(t, st)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/interest/rates?type=policy")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// testObs has 2 policy series and 1 reference series.
	count, _ := body["count"].(float64)
	if int(count) != 2 {
		t.Errorf("type=policy count = %v, want 2", count)
	}
}

func TestInterestRatesFilterAreaAndType(t *testing.T) {
	st, cancel := populatedRateStore(t, testObs, 3)
	defer cancel()
	srv := ratesAPIServer(t, st)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/interest/rates?area=ZA&type=policy")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Only za.policy matches both filters.
	count, _ := body["count"].(float64)
	if int(count) != 1 {
		t.Errorf("area=ZA&type=policy count = %v, want 1", count)
	}
}

func TestInterestRatesResponseHasBuiltAt(t *testing.T) {
	st, cancel := populatedRateStore(t, testObs, 3)
	defer cancel()
	srv := ratesAPIServer(t, st)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/interest/rates")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["built_at"]; !ok {
		t.Error("/interest/rates must include 'built_at' key")
	}
}

// ─── /api/v1/interest/series ─────────────────────────────────────────────────

func TestInterestSeriesKnownID(t *testing.T) {
	st, cancel := populatedRateStore(t, testObs, 3)
	defer cancel()
	srv := ratesAPIServer(t, st)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/interest/series?id=us.policy")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/series?id=us.policy status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["series"]; !ok {
		t.Error("/series response must include 'series' key")
	}
	if _, ok := body["history"]; !ok {
		t.Error("/series response must include 'history' key")
	}
}

func TestInterestSeriesMissingIDIs400(t *testing.T) {
	st := ratestore.New(time.Hour)
	mux := http.NewServeMux()
	New(st, "*").Routes(mux)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/interest/series", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("/series without id: status = %d, want 400", rr.Code)
	}
}

func TestInterestSeriesUnknownIDIs404(t *testing.T) {
	st := ratestore.New(time.Hour)
	mux := http.NewServeMux()
	New(st, "*").Routes(mux)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/interest/series?id=xx.nonexistent", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)

	if rr.Code != http.StatusNotFound {
		t.Errorf("/series unknown id: status = %d, want 404", rr.Code)
	}
}

// ─── /api/v1/interest/meta ───────────────────────────────────────────────────

func TestInterestMetaShape(t *testing.T) {
	st, cancel := populatedRateStore(t, testObs, 3)
	defer cancel()
	srv := ratesAPIServer(t, st)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/interest/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/interest/meta status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"built_at", "areas", "area_count", "series", "sources"} {
		if _, ok := body[key]; !ok {
			t.Errorf("/interest/meta missing key %q", key)
		}
	}

	areas, _ := body["areas"].([]any)
	// testObs has US and ZA → 2 distinct areas.
	if len(areas) != 2 {
		t.Errorf("area count = %d, want 2", len(areas))
	}
}

// ─── CORS headers ────────────────────────────────────────────────────────────

// TestInterestSuccessEndpointsCORSHeaders checks endpoints that always return a
// JSON body. The /series endpoint without an id takes the http.Error path which
// intentionally omits CORS headers and is tested separately as a 400.
func TestInterestSuccessEndpointsCORSHeaders(t *testing.T) {
	st := ratestore.New(time.Hour)
	mux := http.NewServeMux()
	New(st, "*").Routes(mux)

	for _, path := range []string{"/api/v1/interest/rates", "/api/v1/interest/meta"} {
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
