package serve_test

// /readyz exists because /healthz answered 200 before any rate had been
// fetched, and every managed sidecar in sdks/ believed it. These tests pin the
// distinction that mistake turned on: liveness answers when the process is up,
// readiness answers when a conversion would actually succeed.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/fxsource"
	"github.com/vul-os/openrate/serve"
)

func readyProbe(t *testing.T, e *openrate.Engine, opts serve.Options) (int, map[string]any) {
	t.Helper()
	s := serve.New(e, opts)
	t.Cleanup(func() { _ = s.Close() })
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("/readyz body is not JSON: %v (%q)", err, rec.Body.String())
	}
	return rec.Code, body
}

// A fresh engine has an empty snapshot — exactly the window the SDKs raced.
func TestReadyIsNotReadyBeforeAnyRateArrives(t *testing.T) {
	code, body := readyProbe(t, openrate.NewEngine(openrate.EngineOptions{}), serve.Options{})

	if code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz on an empty engine = %d, want 503. This is the whole bug: a "+
			"client that treats this as ready converts against nothing and gets "+
			"'unknown or unreachable currency pair' for every request.", code)
	}
	if body["ready"] != false {
		t.Errorf(`ready = %v, want false`, body["ready"])
	}
	if body["reason"] == nil || body["reason"] == "" {
		t.Error("a 503 must say why; a caller printing a bare timeout is what this replaces")
	}
}

// Liveness must NOT move with readiness, or the distinction is cosmetic.
func TestHealthzStaysUpWhileReadyzIsDown(t *testing.T) {
	s := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{})
	defer func() { _ = s.Close() }()
	h := s.Handler()

	for path, want := range map[string]int{
		"/healthz": http.StatusOK,
		"/readyz":  http.StatusServiceUnavailable,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s = %d, want %d", path, rec.Code, want)
		}
	}
}

func TestReadyTurnsReadyOnceASnapshotHasCurrencies(t *testing.T) {
	e := openrate.NewEngine(openrate.EngineOptions{})
	g := fx.NewGraph()
	g.Replace("test", []fx.Edge{{From: "EUR", To: "ZAR", Rate: 19.5, Source: "test", Time: time.Now()}})
	e.Load(g.Materialize(time.Now()))

	code, body := readyProbe(t, e, serve.Options{})
	if code != http.StatusOK {
		t.Fatalf("/readyz after Load = %d, want 200 (body %v)", code, body)
	}
	if body["ready"] != true {
		t.Errorf("ready = %v, want true", body["ready"])
	}
	if n, _ := body["currencies"].(float64); n < 2 {
		t.Errorf("currencies = %v, want at least the two the edge introduced", body["currencies"])
	}
}

// The reason a caller can act on is the per-source error, not the status code.
func TestNotReadyCarriesTheSourceFailureThatCausedIt(t *testing.T) {
	_, body := readyProbe(t, openrate.NewEngine(openrate.EngineOptions{}), serve.Options{
		Status: func() []fxsource.Status {
			return []fxsource.Status{{Name: "ecb", LastError: "dial tcp: connection refused"}}
		},
	})
	srcs, ok := body["sources"].([]any)
	if !ok || len(srcs) != 1 {
		t.Fatalf("sources = %v, want the one failing source", body["sources"])
	}
	first, _ := srcs[0].(map[string]any)
	if first["last_error"] != "dial tcp: connection refused" {
		t.Errorf("source last_error = %v, want the underlying dial failure verbatim — a caller "+
			"that can only print 'not ready' has to guess", first["last_error"])
	}
}

// guard() rate-limits /api/ only. A client polling readiness must not spend the
// budget it is waiting to use — which is precisely what polling /api/v1/meta,
// the workaround this endpoint removes, did.
func TestReadyzIsNotRateLimited(t *testing.T) {
	s := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{RateLimit: 2})
	defer func() { _ = s.Close() }()
	h := s.Handler()

	for i := 0; i < 30; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("/readyz was rate-limited on poll %d; a readiness probe that "+
				"exhausts the API budget hands the first real call a 429", i+1)
		}
	}

	// Control: the limiter is genuinely on, so the assertion above is not vacuous.
	var limited bool
	for i := 0; i < 30; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("control failed: /api/ never returned 429 at RateLimit=2, so this test " +
			"would pass even with the limiter applied to /readyz")
	}
}
