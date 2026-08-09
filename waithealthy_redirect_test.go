package openrate

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestWaitHealthyDoesNotFollowRedirects covers the one outbound client outside
// the source adapters. It only ever talks to the loopback server this process
// just started, so a redirect out of /healthz is not something our own mux
// emits — but the poller used Go's default policy, which would chase one to any
// host, turning startup into an outbound request the embedding host never asked
// for. Refusing is free here: a 3xx simply is not 200, so the poll keeps waiting.
func TestWaitHealthyDoesNotFollowRedirects(t *testing.T) {
	var hits atomic.Int64
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ok")) // a 200, so following the redirect would "succeed"
	}))
	defer elsewhere.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/healthz", http.StatusFound)
	}))
	defer redirector.Close()

	err := waitHealthy(redirector.URL, 250*time.Millisecond)
	if err == nil {
		t.Fatal("waitHealthy accepted a redirected health check")
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("waitHealthy followed the redirect: the other host was contacted %d time(s)", got)
	}
}

// And the ordinary case still works, so the policy above cannot pass by making
// every health check fail.
func TestWaitHealthyAcceptsAHealthyServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	if err := waitHealthy(srv.URL, 2*time.Second); err != nil {
		t.Fatalf("waitHealthy on a healthy server: %v", err)
	}
}
