//go:build noui

package embedtest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/serve"
	"github.com/vul-os/openrate/serve/web"
)

// uiSentinel must never appear in a noui build. It is declared in both tagged
// files so each build state carries exactly one definition.
const uiSentinel = `id="board-table"`

// TestG5NoUIBuildServesTheJSONStub is the tagged half of the pair in
// g5_ui_default_test.go. An embedder builds with -tags noui to drop the
// console; this asserts the drop is real from the outside — the API is
// untouched, "/" answers JSON, and nothing of the page comes back.
func TestG5NoUIBuildServesTheJSONStub(t *testing.T) {
	if web.Embedded {
		t.Fatal("web.Embedded is true in a build tagged noui")
	}

	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	api := serve.New(e, serve.Options{UI: true}) // UI requested and still absent
	t.Cleanup(func() { _ = api.Close() })

	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200 (a console-less build must still say what it is)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("GET / Content-Type = %q, want application/json", ct)
	}
	body := rec.Body.String()
	if strings.Contains(body, uiSentinel) || strings.Contains(strings.ToLower(body), "<!doctype html>") {
		t.Errorf("GET / served the console in a build that is supposed not to have one: %s", body)
	}
	var stub struct {
		Service   string   `json:"service"`
		API       string   `json:"api"`
		Endpoints []string `json:"endpoints"`
	}
	if err := json.Unmarshal([]byte(body), &stub); err != nil {
		t.Fatalf("GET / is not valid JSON (%v): %s", err, body)
	}
	if stub.Service != "openrate" || stub.API != "/api/v1" || len(stub.Endpoints) < 4 {
		t.Errorf("the stub does not identify the service and its API: %+v", stub)
	}

	assertAPIStillAnswers(t, api)
}
