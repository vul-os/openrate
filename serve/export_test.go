package serve

import "net/http"

// WriteJSONForTest exposes the one encoder every response goes through, so a
// test can hand it a value json cannot represent. Test-only: this file is
// `_test.go`, so it is not part of the package's public surface.
func WriteJSONForTest(w http.ResponseWriter, v any) {
	(&Server{cors: "*"}).writeJSON(w, v)
}
