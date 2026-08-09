package openrate

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/vul-os/openrate/fxsource"
	"github.com/vul-os/openrate/internal/ratesources"
	"github.com/vul-os/openrate/internal/ratestore"
	"github.com/vul-os/openrate/serve"
	"github.com/vul-os/openrate/serve/interest"
)

// Options configures the embedded engine.
//
// Deprecated: Options belongs to [Start]. Use [EngineOptions], [RefreshOptions]
// and serve.Options, which say separately what to compute, what to fetch and
// what to serve.
type Options struct {
	// Addr overrides the listen address. If empty, an ephemeral localhost port
	// is chosen.
	Addr string
	// Base is the default presentation base currency (default "ZAR").
	Base string
	// Refresh is the source refresh interval (default 1h).
	Refresh time.Duration
	// Sources is a comma-separated source spec (e.g. "ecb,coinbase"). If empty,
	// the default set is used (see fxsource.Build).
	Sources string
	// Interest, when true, also runs the interest-rate engine and mounts its
	// routes at /api/v1/interest/*. Off by default; the FX engine is unaffected.
	Interest bool
	// InterestSources is a comma-separated interest-rate source spec (e.g.
	// "bis,sarbrates,fred"). Empty uses ratesources.DefaultSources.
	InterestSources string
	// InterestRefresh is the interest-rate refresh interval (default 6h).
	InterestRefresh time.Duration
	// RateLimit sets per-IP API requests/minute. 0 (the default) disables it —
	// the anti-scraping limiter is rarely wanted for an embedded engine.
	RateLimit int
	// ServeUI mounts the embedded UI at "/". Off by default; embedders usually
	// want only the JSON API.
	ServeUI bool
	// CORSOrigin sets Access-Control-Allow-Origin on JSON responses. Empty
	// defaults to "*" (public, read-only); set a specific origin to lock down.
	CORSOrigin string
	// TrustedProxies lists proxy IPs/CIDRs whose X-Forwarded-For is honored for
	// rate-limiting. Empty (the default) never trusts XFF and uses RemoteAddr.
	TrustedProxies []string
	// ReadyTimeout bounds how long Start waits for the server to serve
	// (default 10s).
	ReadyTimeout time.Duration
}

// Local is a running in-process engine.
//
// Deprecated: Local is [Start]'s handle. See [Start].
type Local struct {
	// BaseURL is the http://host:port root (no trailing slash).
	BaseURL string
	cancel  context.CancelFunc
	srv     *http.Server
	api     *serve.Server
	done    chan struct{}
}

// Start builds the engine, launches it in a background goroutine, and returns
// once it is serving (the /healthz endpoint responds OK).
//
// Deprecated: Start makes a Go program talk HTTP to itself. It binds a loopback
// listener, starts a refresh loop, and begins fetching from the network before
// it returns — none of which a caller that only wants to convert a number asked
// for, and none of which could be turned off.
//
// Use [NewEngine] instead, which computes and nothing else:
//
//	e := openrate.NewEngine(openrate.EngineOptions{})
//	e.Load(snapshot) // from a file, a cache, another process — or a Refresher
//	c, err := e.Convert("USD", "ZAR", 100)
//
// Add [NewRefresher] when — and only when — the process should fetch, and
// serve.New when it should also answer HTTP. Start keeps working, built on
// exactly those three pieces, so nothing that depends on it breaks.
func Start(opts Options) (*Local, error) {
	srcs := fxsource.FromEnvSpec(opts.Sources)
	if len(srcs) == 0 {
		return nil, fmt.Errorf("openrate: no valid sources configured (spec=%q)", opts.Sources)
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

	engine := NewEngine(EngineOptions{Base: opts.Base})
	refresher := NewRefresher(engine, RefreshOptions{Sources: srcs, Interval: opts.Refresh})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = refresher.Run(ctx) }()

	var extra []serve.Routable
	if opts.Interest {
		intRefresh := opts.InterestRefresh
		if intRefresh <= 0 {
			intRefresh = 6 * time.Hour
		}
		if intSrcs := ratesources.Build(opts.InterestSources); len(intSrcs) > 0 {
			ist := ratestore.New(intRefresh, nil, intSrcs...)
			go ist.Run(ctx)
			extra = append(extra, interest.New(ist, opts.CORSOrigin))
		}
	}

	api := serve.New(engine, serve.Options{
		UI:             opts.ServeUI,
		CORSOrigin:     opts.CORSOrigin,
		RateLimit:      opts.RateLimit,
		TrustedProxies: opts.TrustedProxies,
		Status:         refresher.Status,
		Extra:          extra,
	})

	l := &Local{
		BaseURL: "http://" + addr,
		cancel:  cancel,
		srv:     &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second},
		api:     api,
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
	// Note what this waits for: the listener answering, not a rate existing.
	// That was always true of Start, and is why Refresher.Ready exists.
	if err := waitHealthy(l.BaseURL, timeout); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// APIBaseURL returns the .../api/v1 base URL for the JSON API.
func (l *Local) APIBaseURL() string { return l.BaseURL + "/api/v1" }

// Close stops refreshing, stops the rate-limiter sweeper (if active), shuts the
// server down, and waits for it to stop.
func (l *Local) Close() error {
	l.cancel()
	_ = l.api.Close()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := l.srv.Shutdown(shutCtx)
	<-l.done
	return err
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
