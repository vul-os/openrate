//go:build noui

// This file is the whole of package web in a build tagged `noui`. ui.html and
// THIRD-PARTY-NOTICES.txt are not embedded, so the binary carries none of those
// bytes and the console does not exist. The JSON API is untouched: openrate's
// reason to exist is the rates, and the console is a convenience over them.
//
// The stub is deliberately not a 404. A build without a console still wants to
// answer "what is this thing and where is its API" to whoever opens the root
// URL — a silent 404 there reads as a broken deployment.
package web

import (
	"net/http"
)

// Embedded reports whether this build carries the console. See embed.go for the
// default (untagged) build, where it is true.
const Embedded = false

const stub = `{
  "service": "openrate",
  "ui": "not included in this build (compiled with -tags noui)",
  "api": "/api/v1",
  "endpoints": ["/api/v1/rates", "/api/v1/convert", "/api/v1/meta", "/healthz"]
}
`

const noticesStub = `This binary was built with -tags noui, which drops the embedded copy of
THIRD-PARTY-NOTICES.txt along with the web console. The notices for this
build are the same ones shipped in the source distribution: see
THIRD-PARTY-NOTICES.txt at the root of the openrate repository.
`

// Handler answers the same paths the console would have, with a small JSON
// document instead of the page. Method handling matches the embedded build so
// that swapping the tag cannot change how a proxy or probe sees the service.
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
			_, _ = w.Write([]byte(noticesStub))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(stub))
	})
}
