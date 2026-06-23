package openrate_test

import (
	"net/http"
	"testing"

	"github.com/vul-os/openrate"
)

func TestStartServesAndCloses(t *testing.T) {
	local, err := openrate.Start(openrate.Options{Sources: "ecb"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer local.Close()

	resp, err := http.Get(local.BaseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}

	// The API mux is wired in-process; /meta should answer (200) even before a
	// snapshot has been fetched.
	resp, err = http.Get(local.APIBaseURL() + "/meta")
	if err != nil {
		t.Fatalf("GET /api/v1/meta: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("/api/v1/meta returned 404; API not wired")
	}

	if err := local.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestStartNoSources(t *testing.T) {
	if _, err := openrate.Start(openrate.Options{Sources: "nonexistent-source"}); err == nil {
		t.Fatal("expected error for empty source set, got nil")
	}
}
