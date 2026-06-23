// Package openrate embeds the rate engine in-process for Go programs.
//
// The openrate binary (cmd/openrate) keeps its building blocks under internal/,
// so they cannot be imported directly. This package is the public surface: it
// wires the same store, sources, API, and hardening that main() does, but runs
// them in a background goroutine so any Go program can embed the engine without
// shelling out to the binary:
//
//	local, err := openrate.Start(openrate.Options{})
//	if err != nil { ... }
//	defer local.Close()
//	// hit local.APIBaseURL()+"/rates", or local.BaseURL+"/healthz", etc.
//
// Provider API keys (for the sources that need them) are read from the
// environment, exactly as the binary reads them.
package openrate

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vul-os/openrate/internal/api"
	"github.com/vul-os/openrate/internal/ratelimit"
	"github.com/vul-os/openrate/internal/sources"
	"github.com/vul-os/openrate/internal/store"
	"github.com/vul-os/openrate/web"
)

// Options configures the embedded engine. The zero value is valid and mirrors
// the binary's defaults (ZAR base, hourly refresh, the default source set, no
// rate limiting, no UI).
type Options struct {
	// Addr overrides the listen address. If empty, an ephemeral localhost port
	// is chosen.
	Addr string
	// Base is the default presentation base currency (default "ZAR").
	Base string
	// Refresh is the source refresh interval (default 1h).
	Refresh time.Duration
	// Sources is a comma-separated source spec (e.g. "ecb,coinbase"). If empty,
	// the default set is used (see sources.Build).
	Sources string
	// RateLimit sets per-IP API requests/minute. 0 (the default) disables it —
	// the anti-scraping limiter is rarely wanted for an embedded engine.
	RateLimit int
	// ServeUI mounts the embedded React UI at "/". Off by default; embedders
	// usually want only the JSON API.
	ServeUI bool
	// ReadyTimeout bounds how long Start waits for the server to serve
	// (default 10s).
	ReadyTimeout time.Duration
}

// Local is a running in-process engine.
type Local struct {
	// BaseURL is the http://host:port root (no trailing slash).
	BaseURL string
	cancel  context.CancelFunc
	srv     *http.Server
	done    chan struct{}
}

// Start builds the engine, launches it in a background goroutine, and returns
// once it is serving (the /healthz endpoint responds OK).
func Start(opts Options) (*Local, error) {
	srcs := sources.Build(opts.Sources)
	if len(srcs) == 0 {
		return nil, fmt.Errorf("openrate: no valid sources configured (spec=%q)", opts.Sources)
	}

	base := opts.Base
	if base == "" {
		base = "ZAR"
	}
	refresh := opts.Refresh
	if refresh <= 0 {
		refresh = time.Hour
	}
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	addr = ln.Addr().String()

	st := store.New(refresh, srcs...)
	ctx, cancel := context.WithCancel(context.Background())
	go st.Run(ctx)

	mux := http.NewServeMux()
	api.New(st, base).Routes(mux)
	if opts.ServeUI {
		if sub, err := web.FS(); err == nil {
			mux.Handle("/", http.FileServer(http.FS(sub)))
		}
	}

	var limiter *ratelimit.Limiter
	if opts.RateLimit > 0 {
		limiter = ratelimit.New(opts.RateLimit, opts.RateLimit/2+1)
	}

	l := &Local{
		BaseURL: "http://" + addr,
		cancel:  cancel,
		srv:     &http.Server{Addr: addr, Handler: guard(mux, limiter), ReadHeaderTimeout: 10 * time.Second},
		done:    make(chan struct{}),
	}

	go func() {
		defer close(l.done)
		_ = l.srv.Serve(ln)
	}()

	timeout := opts.ReadyTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if err := waitHealthy(l.BaseURL, timeout); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// APIBaseURL returns the .../api/v1 base URL for the JSON API.
func (l *Local) APIBaseURL() string { return l.BaseURL + "/api/v1" }

// Close stops refreshing, shuts the server down, and waits for it to stop.
func (l *Local) Close() error {
	l.cancel()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := l.srv.Shutdown(shutCtx)
	<-l.done
	return err
}

// guard mirrors cmd/openrate's hardening: a restrictive robots.txt, security
// headers, no-store caching on the API, and optional per-IP rate limiting on
// /api/ paths. The embedded UI and its assets are not rate-limited.
func guard(mux http.Handler, limiter *ratelimit.Limiter) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /api/\n"))
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if limiter != nil {
				limiter.Middleware(mux).ServeHTTP(w, r)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func waitHealthy(base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	var last error
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		} else {
			last = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("openrate did not become healthy within %s: %v", timeout, last)
}
