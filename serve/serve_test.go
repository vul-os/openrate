package serve_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/fxsource"
	"github.com/vul-os/openrate/serve"
	"github.com/vul-os/openrate/serve/web"
)

// TestNewStartsNothing: building a Server must not start a goroutine. The rate
// limiter's sweeper is the one background thing in this package, and it starts
// with Handler(), not with New() — so a caller that only wants Routes() on its
// own mux never acquires it.
func TestNewStartsNothing(t *testing.T) {
	runtime.GC()
	before := runtime.NumGoroutine()

	s := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{RateLimit: 60})
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("serve.New started %d goroutine(s)", after-before)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close on a server whose Handler was never built: %v", err)
	}
}

// The UI is optional and off unless asked for: importing serve must not put a
// console on an embedder's "/" by surprise.
func TestUIOffByDefault(t *testing.T) {
	s := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{})
	mux := http.NewServeMux()
	s.Routes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET / with UI off: status = %d, want 404 (nothing should be mounted there)", rec.Code)
	}
	// The API is unaffected.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/meta with UI off: status = %d, want 200", rec.Code)
	}
}

// With UI on, "/" answers — with the console in a normal build and with the
// JSON stub in one tagged `noui`. Asserting on web.Embedded rather than on a
// fixed body is what lets this test mean something in both build states.
func TestUIMountedWhenAsked(t *testing.T) {
	s := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{UI: true})
	mux := http.NewServeMux()
	s.Routes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / with UI on: status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	body := rec.Body.String()
	if web.Embedded {
		if !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("GET /: Content-Type = %q, want text/html in a build with the console", ct)
		}
		if !strings.Contains(body, "<!doctype html>") {
			t.Fatal("GET /: the console build did not serve the page")
		}
	} else {
		if !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("GET /: Content-Type = %q, want application/json in a noui build", ct)
		}
		if !strings.Contains(body, "/api/v1") {
			t.Fatal("GET /: the noui stub does not point at the API")
		}
	}
}

func TestRobotsTxtDisallowsTheAPI(t *testing.T) {
	s := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{})
	defer s.Close()

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /robots.txt: status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Disallow: /api/") {
		t.Fatalf("robots.txt does not disallow the API: %q", rec.Body.String())
	}
}

// The hardening used to be duplicated in cmd/openrate and in openrate.Start,
// and was covered by neither. It has one implementation now, and this is it.
func TestHandlerHardening(t *testing.T) {
	s := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{})
	defer s.Close()
	h := s.Handler()

	for _, tc := range []struct {
		path        string
		wantNoStore bool
	}{
		{"/api/v1/meta", true},
		{"/healthz", false},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", tc.path, got)
		}
		if got := rec.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
			t.Errorf("%s: Referrer-Policy = %q", tc.path, got)
		}
		gotNoStore := rec.Header().Get("Cache-Control") == "no-store"
		if gotNoStore != tc.wantNoStore {
			t.Errorf("%s: Cache-Control no-store = %v, want %v", tc.path, gotNoStore, tc.wantNoStore)
		}
	}
}

// Rate limiting applies to /api/ and not to the console — a page that stops
// loading because a user refreshed it is not anti-scraping.
func TestRateLimitAppliesToTheAPIOnly(t *testing.T) {
	s := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{RateLimit: 2, UI: true})
	defer s.Close()
	h := s.Handler()

	limited := false
	for range 20 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("20 rapid API requests at 2/min were never rate-limited")
	}

	for i := range 10 {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("console request %d was rate-limited; only /api/ should be", i)
		}
	}
}

// /meta's source list comes from whatever the deployment plugged in — a
// Refresher's Status method, normally. With nothing plugged in it must be an
// empty list, not null: a client iterating the field should not have to
// nil-check a fact about this deployment.
func TestMetaSourcesComeFromTheStatusHook(t *testing.T) {
	stat := []fxsource.Status{{Name: "ecb", Edges: 31}, {Name: "sarb", Edges: 1}}
	s := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{
		Status: func() []fxsource.Status { return stat },
	})
	mux := http.NewServeMux()
	s.Routes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	var body struct {
		Sources []fxsource.Status `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /meta: %v", err)
	}
	if len(body.Sources) != 2 || body.Sources[0].Name != "ecb" || body.Sources[0].Edges != 31 {
		t.Fatalf("sources = %+v, want the two the hook reported", body.Sources)
	}

	bare := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{})
	mux = http.NewServeMux()
	bare.Routes(mux)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	if !strings.Contains(rec.Body.String(), `"sources": []`) {
		t.Fatalf("with no status hook, /meta must report an empty source list, got: %s", rec.Body.String())
	}
}

// A route set handed in through Extra is mounted on the same mux — this is how
// the interest-rate API and anything a host adds get served.
type stubRoutable struct{ path string }

func (s stubRoutable) Routes(mux *http.ServeMux) {
	mux.HandleFunc(s.path, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("extra")) })
}

func TestExtraRoutesAreMounted(t *testing.T) {
	s := serve.New(openrate.NewEngine(openrate.EngineOptions{}), serve.Options{
		Extra: []serve.Routable{stubRoutable{"/api/v1/interest/rates"}},
	})
	mux := http.NewServeMux()
	s.Routes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/interest/rates", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "extra" {
		t.Fatalf("extra route: status %d body %q", rec.Code, rec.Body.String())
	}
}

// The engine serve reads from is an interface, so a host can serve a snapshot
// it obtained any way it likes. This is the whole "serve is a shell" claim,
// checked rather than asserted.
type staticEngine struct{ e *openrate.Engine }

func (s staticEngine) DefaultBase() string    { return "USD" }
func (s staticEngine) Snapshot() *fx.Snapshot { return s.e.Snapshot() }

func TestServeAcceptsAnyEngineImplementation(t *testing.T) {
	e := populatedEngine(t, testEdges, 3)
	s := serve.New(staticEngine{e}, serve.Options{})
	mux := http.NewServeMux()
	s.Routes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/convert?to=ZAR&amount=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("convert through a custom engine: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		From   string  `json:"from"`
		Result float64 `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.From != "USD" {
		t.Errorf("from = %q — the custom engine's DefaultBase was ignored", body.From)
	}
	if body.Result != 37 {
		t.Errorf("2 USD→ZAR = %v, want 37", body.Result)
	}
}

// TestLegWireShapeIsStable pins the exact keys of a leg on the wire. The
// refactor broke this once: fx.Leg carries the quote's `time`, the API has
// always published an `age_sec` instead, and returning the library type
// directly silently changed both. Every consumer reading legs[].age_sec would
// have got nothing, with a 200 and valid JSON to hide it.
func TestLegWireShapeIsStable(t *testing.T) {
	// An hour after the quotes, so a leg's age is a number this test can pin
	// rather than the zero any broken computation would also produce.
	const ageSec = 3600
	mux := http.NewServeMux()
	serve.New(populatedEngine(t, testEdges, 3), serve.Options{
		Now: func() time.Time { return apiTestTime.Add(ageSec * time.Second) },
	}).Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/convert?from=EUR&to=ZAR&amount=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body struct {
		Rate struct {
			Hops int              `json:"hops"`
			Legs []map[string]any `json:"legs"`
		} `json:"rate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Rate.Hops != 2 || len(body.Rate.Legs) != 2 {
		t.Fatalf("EUR→ZAR should triangulate through USD: hops %d, %d legs — the fixture is not exercising multiple legs",
			body.Rate.Hops, len(body.Rate.Legs))
	}
	want := map[string]bool{"from": true, "to": true, "rate": true, "source": true, "age_sec": true}
	for i, leg := range body.Rate.Legs {
		for k := range leg {
			if !want[k] {
				t.Errorf("leg %d carries an unexpected key %q (the wire shape is from/to/rate/source/age_sec)", i, k)
			}
		}
		for k := range want {
			if _, ok := leg[k]; !ok {
				t.Errorf("leg %d is missing %q", i, k)
			}
		}
		// The key being present is not enough: it has to carry the leg's real
		// age. A hard-coded or forgotten computation shows up as 0 here.
		if age, ok := leg["age_sec"].(float64); !ok || age != ageSec {
			t.Errorf("leg %d age_sec = %v, want %d (the quote is an hour old on this clock)", i, leg["age_sec"], ageSec)
		}
	}
}
