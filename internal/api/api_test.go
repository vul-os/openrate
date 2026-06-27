package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vul-os/openrate/internal/store"
)

func TestCORSVaryHeader(t *testing.T) {
	cases := []struct {
		name     string
		origin   string
		wantACAO string
		wantVary bool
	}{
		{"wildcard", "*", "*", false},
		{"specific", "https://app.example.com", "https://app.example.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(store.New(time.Hour), "ZAR", tc.origin)
			mux := http.NewServeMux()
			s.Routes(mux)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			if got := rr.Header().Get("Access-Control-Allow-Origin"); got != tc.wantACAO {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tc.wantACAO)
			}
			gotVary := rr.Header().Get("Vary") == "Origin"
			if gotVary != tc.wantVary {
				t.Errorf("Vary: Origin present = %v, want %v (Vary=%q)", gotVary, tc.wantVary, rr.Header().Get("Vary"))
			}
		})
	}
}
