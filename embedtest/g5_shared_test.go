package embedtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vul-os/openrate/serve"
)

// assertAPIStillAnswers is the half of the server that must be IDENTICAL in
// both build states. The noui tag drops a console, not an API; a build where
// dropping the page also changed how /api/v1/meta answers would be a different
// service wearing the same name.
//
// Both g5 tests call it, so the shared expectation lives in one place and
// neither tagged file can drift away from it on its own.
func assertAPIStillAnswers(t *testing.T, api *serve.Server) {
	t.Helper()
	h := api.Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Errorf("GET /healthz = %d %q, want 200 \"ok\"", rec.Code, rec.Body.String())
	}

	for _, path := range []string{"/api/v1/rates", "/api/v1/meta"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s Content-Type = %q, want application/json", path, ct)
		}
		var any map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &any); err != nil {
			t.Errorf("GET %s is not a JSON object (%v): %s", path, err, rec.Body.String())
		}
	}
}
