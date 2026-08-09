//go:build !noui

package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A coverage floor so these tests cannot pass by checking nothing: the real
// ui.html is ~34KB. A placeholder, a truncated write, or an accidentally
// emptied file must fail loudly here.
const minUISize = 15000

func TestUIEmbedded(t *testing.T) {
	if len(UI) < minUISize {
		t.Fatalf("embedded UI is %d bytes, want at least %d — looks like a placeholder, not the real thing", len(UI), minUISize)
	}
}

// TestUIContainsCoreElements pins the concrete markup/script the product
// depends on, so a careless edit that drops the converter, the board, or the
// API calls they issue fails a Go test instead of only being noticed by eye.
func TestUIContainsCoreElements(t *testing.T) {
	s := string(UI)
	must := []string{
		"<!doctype html>",
		`id="convert"`,         // the converter section
		`id="board"`,           // the rates board
		`id="amount"`,          // amount input
		`id="from"`,            // from-currency select
		`id="to"`,              // to-currency select
		`id="result"`,          // converted output
		`id="board-table"`,     // the all-pairs table
		"/api/v1/rates",        // board data
		"/api/v1/convert",      // converter data
		"/api/v1/meta",         // currency + source list
		"or-theme",             // shared localStorage key with the marketing site
		"prefers-color-scheme", // system theme fallback
		"<svg",                 // inline brand mark, not a linked image
		"/licenses.txt",        // link to the third-party notices the binary serves
	}
	for _, m := range must {
		if !strings.Contains(s, m) {
			t.Errorf("embedded UI missing expected content: %q", m)
		}
	}

	// No in-binary landing page or docs viewer: nothing marketing-flavoured or
	// documentation-flavoured should have crept back in.
	mustNot := []string{"docs-layout", "Quick start", "hero-plate", "guilloche"}
	for _, m := range mustNot {
		if strings.Contains(s, m) {
			t.Errorf("embedded UI contains %q — looks like landing/docs content leaked back in", m)
		}
	}
}

// TestUINoExternalOrigin is the property that actually matters for an
// offline-embedded UI: nothing in the page may fetch from another origin —
// no CDN script/style, no remote font, no tracking pixel. The page is fully
// self-contained (inline CSS, inline JS, a data: URI favicon), so the
// strongest and simplest check is that no "http://" or "https://" substring
// appears in the document at all.
func TestUINoExternalOrigin(t *testing.T) {
	s := string(UI)
	if i := strings.Index(s, "http://"); i != -1 {
		t.Errorf("embedded UI references an external http:// origin near byte %d: %q", i, snippet(s, i))
	}
	if i := strings.Index(s, "https://"); i != -1 {
		t.Errorf("embedded UI references an external https:// origin near byte %d: %q", i, snippet(s, i))
	}

	// Defence in depth: even if some future edit adds a scheme-relative or
	// protocol-less reference, common CDN/font hosts must never appear.
	cdns := []string{"cdn.", "jsdelivr", "unpkg.com", "cdnjs", "googleapis.com", "gstatic.com"}
	lower := strings.ToLower(s)
	for _, c := range cdns {
		if strings.Contains(lower, c) {
			t.Errorf("embedded UI references a CDN-looking host: %q", c)
		}
	}
}

// TestUIBothThemes checks both the light and dark CSS token sets are present
// (prefers-color-scheme AND the explicit [data-theme] override the toggle
// writes), since the requirement is that the UI follows the system theme AND
// respects a stored preference.
func TestUIBothThemes(t *testing.T) {
	s := string(UI)
	if !strings.Contains(s, `[data-theme="light"]`) {
		t.Error("embedded UI has no explicit light theme override")
	}
	if !strings.Contains(s, "@media (prefers-color-scheme:light)") && !strings.Contains(s, "@media (prefers-color-scheme: light)") {
		t.Error("embedded UI has no prefers-color-scheme fallback")
	}
}

func snippet(s string, i int) string {
	end := i + 60
	if end > len(s) {
		end = len(s)
	}
	return s[i:end]
}

// A coverage floor for the notices file, mirroring minUISize: the real file
// is ~20KB (Go stdlib licence + patent grant alone are several KB).
const minLicensesSize = 5000

func TestLicensesEmbedded(t *testing.T) {
	if len(Licenses) < minLicensesSize {
		t.Fatalf("embedded THIRD-PARTY-NOTICES.txt is %d bytes, want at least %d — looks empty or truncated", len(Licenses), minLicensesSize)
	}
	if !strings.Contains(string(Licenses), "THIRD-PARTY NOTICES") {
		t.Error("embedded licenses file doesn't look like THIRD-PARTY-NOTICES.txt")
	}
}

// TestLicensesInSyncWithRoot is the staleness guard go:embed itself cannot
// provide: an embed pattern may not contain ".." (embed.go can't reach the
// repo-root THIRD-PARTY-NOTICES.txt directly), so web/THIRD-PARTY-NOTICES.txt
// is a committed copy. If scripts/gen-notices.sh regenerates the root file
// and nobody re-copies it here, the binary would silently serve outdated
// attribution — this test fails loudly instead.
func TestLicensesInSyncWithRoot(t *testing.T) {
	root, err := os.ReadFile(filepath.Join("..", "..", "THIRD-PARTY-NOTICES.txt"))
	if err != nil {
		t.Fatalf("cannot read repo-root THIRD-PARTY-NOTICES.txt: %v", err)
	}
	if !bytes.Equal(root, Licenses) {
		t.Fatalf("serve/web/THIRD-PARTY-NOTICES.txt has drifted from the repo-root copy. "+
			"Re-run scripts/gen-notices.sh, then `cp THIRD-PARTY-NOTICES.txt serve/web/THIRD-PARTY-NOTICES.txt` "+
			"(root is %d bytes, web copy is %d bytes)", len(root), len(Licenses))
	}
}

func TestHandlerServesLicenses(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodGet, "/licenses.txt", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /licenses.txt: status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("GET /licenses.txt: Content-Type = %q, want text/plain", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), Licenses) {
		t.Fatal("GET /licenses.txt: body does not match the embedded notices byte-for-byte")
	}
}

func TestHandlerServesUI(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/", "/anything", "/deep/path"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200", path, rec.Code)
		}
		ct := rec.Header().Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("GET %s: Content-Type = %q, want text/html", path, ct)
		}
		if !bytes.Equal(rec.Body.Bytes(), UI) {
			t.Fatalf("GET %s: body does not match the embedded UI byte-for-byte", path)
		}
	}
}

func TestHandlerMethodNotAllowed(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /: status = %d, want 405", rec.Code)
	}
}

func TestHandlerHead(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest(http.MethodHead, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD /: status = %d, want 200", rec.Code)
	}
}

// TestUIHTMLBalanced is a cheap sanity check that catches a corrupted embed
// (e.g. a bad string edit that leaves a dangling tag) without needing a full
// HTML parser: every opening tag this file uses has a matching close.
func TestUIHTMLBalanced(t *testing.T) {
	s := string(UI)
	for _, tag := range []string{"html", "head", "body", "style", "script", "main", "table", "select"} {
		open := regexp.MustCompile(`<`+tag+`[ >]`).FindAllStringIndex(s, -1)
		closed := strings.Count(s, "</"+tag+">")
		if len(open) != closed {
			t.Errorf("unbalanced <%s>: %d open, %d close", tag, len(open), closed)
		}
	}
}
