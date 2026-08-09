//go:build !noui

package embedtest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/serve"
	"github.com/vul-os/openrate/serve/web"
)

// uiSentinel is a string that exists only inside the console's markup. It is
// the same sentinel scripts/check-ui-builds.sh and scripts/check-embed-linkage.sh
// grep for in a linked binary, so all three guards agree on what "the UI is in
// here" means. If it is ever renamed in serve/web/ui.html, this test fails
// first and says so — a sentinel that no longer occurs anywhere would make the
// binary greps pass forever.
const uiSentinel = `id="board-table"`

// TestG5DefaultBuildServesTheConsole is the untagged half of the pair. Its
// counterpart in g5_ui_noui_test.go asserts the opposite, and both are run in
// CI, so neither build state can quietly stop compiling or change what it
// answers at "/".
func TestG5DefaultBuildServesTheConsole(t *testing.T) {
	if !web.Embedded {
		t.Fatal("web.Embedded is false in an untagged build")
	}
	if !strings.Contains(string(web.UI), uiSentinel) {
		t.Fatalf("the embedded console no longer contains %s. That string is what "+
			"scripts/check-ui-builds.sh and scripts/check-embed-linkage.sh grep for in a "+
			"linked binary; with it gone they would both report clean forever. Pick a new "+
			"sentinel in all three places.", uiSentinel)
	}

	e := openrate.NewEngine(openrate.EngineOptions{Logger: quiet()})
	api := serve.New(e, serve.Options{UI: true})
	t.Cleanup(func() { _ = api.Close() })

	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("GET / Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), uiSentinel) {
		t.Errorf("GET / did not serve the console (no %s in the body)", uiSentinel)
	}

	// The API is the same in both build states; only "/" differs.
	assertAPIStillAnswers(t, api)
}
