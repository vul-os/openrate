// Package web embeds the UI: one hand-written HTML document (ui.html) with an
// inline <style> and inline <script> — no npm, no bundler, no build step. The
// whole embedded artifact is committed source, so a clone runs the exact UI a
// browser will load with nothing to regenerate.
//
// It also embeds THIRD-PARTY-NOTICES.txt, served alongside the page at
// /licenses.txt, so a release binary running with no internet access still
// carries its own attribution — the same file scripts/gen-notices.sh writes
// to the repo root and copies to site/licenses.txt for the marketing site.
// go:embed cannot reach outside the package directory (no ".." in a pattern),
// so web/THIRD-PARTY-NOTICES.txt is a committed copy of the root file;
// embed_test.go asserts the two are byte-identical so the embedded copy
// cannot silently go stale.
package web

import (
	_ "embed"
	"net/http"
)

//go:embed ui.html
var UI []byte

//go:embed THIRD-PARTY-NOTICES.txt
var Licenses []byte

// Handler serves the embedded single-page UI, plus its third-party notices at
// /licenses.txt. There is exactly one HTML document and no client-side
// routing, so every other GET/HEAD request — whatever the path — gets the
// page; the page itself talks to /api/v1/* directly.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/licenses.txt" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(Licenses)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(UI)
	})
}
