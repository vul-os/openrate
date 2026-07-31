// Package web embeds the UI: one hand-written HTML document (ui.html) with an
// inline <style> and inline <script> — no npm, no bundler, no build step. The
// whole embedded artifact is committed source, so a clone runs the exact UI a
// browser will load with nothing to regenerate.
package web

import (
	_ "embed"
	"net/http"
)

//go:embed ui.html
var UI []byte

// Handler serves the embedded single-page UI. There is exactly one HTML
// document and no client-side routing, so every GET/HEAD request — whatever
// the path — gets the same page; the page itself talks to /api/v1/* directly.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(UI)
	})
}
