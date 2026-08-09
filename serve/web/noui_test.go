//go:build noui

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests only exist in the tagged build. They are the reason `go test
// -tags noui ./...` is worth running in CI: a build tag that nothing exercises
// is a build tag that quietly stops compiling.

func TestNoUIBuildHasNoConsole(t *testing.T) {
	if Embedded {
		t.Fatal("Embedded is true in a build tagged noui")
	}
}

func TestStubIsJSONAndNamesTheAPI(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/", "/anything", "/deep/path"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("GET %s: Content-Type = %q, want application/json", path, ct)
		}
		var body struct {
			Service   string   `json:"service"`
			API       string   `json:"api"`
			Endpoints []string `json:"endpoints"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("GET %s: body is not valid JSON (%v): %s", path, err, rec.Body.String())
		}
		if body.Service != "openrate" || body.API != "/api/v1" {
			t.Fatalf("GET %s: stub does not identify the service or its API: %+v", path, body)
		}
		if len(body.Endpoints) < 4 {
			t.Fatalf("GET %s: stub lists %d endpoints, want the full API", path, len(body.Endpoints))
		}
		// The console must be genuinely gone, not merely unlinked.
		if strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Fatalf("GET %s: the noui build served HTML", path)
		}
	}
}

// A build without the console still has to say something honest about
// attribution rather than 404 the notices path.
func TestStubServesNoticesPointer(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/licenses.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /licenses.txt: status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("GET /licenses.txt: Content-Type = %q, want text/plain", ct)
	}
	if !strings.Contains(rec.Body.String(), "THIRD-PARTY-NOTICES.txt") {
		t.Fatal("the notices stub does not say where the real notices are")
	}
}

// Method handling must not differ between the two builds: a probe that gets a
// 405 from one and a 200 from the other is a deployment surprise.
func TestStubMethodNotAllowed(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /: status = %d, want 405", rec.Code)
	}
}
