// Command openrate runs the rate engine: it ingests open central-bank/venue
// sources into a currency graph, materializes an all-pairs snapshot anchored on
// ZAR, and serves a JSON API plus a hand-written, dependency-free embedded UI
// from a single binary.
//
// It is a thin main over the library: an openrate.Engine to compute, an
// openrate.Refresher to fetch, a serve.Server to answer. Everything this file
// does, an embedding program can do — and can choose not to do.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fxsource"
	"github.com/vul-os/openrate/internal/ratesources"
	"github.com/vul-os/openrate/internal/ratestore"
	"github.com/vul-os/openrate/serve"
	"github.com/vul-os/openrate/serve/interest"
)

// Version is set at build time via -ldflags "-X main.Version=vX.Y.Z".
// It defaults to "dev" for local builds.
var Version = "dev"

func main() {
	// One-shot: print version and exit.
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("openrate", Version)
		return
	}
	loadDotEnv(".env")
	addr := flag.String("addr", env("OPENRATE_ADDR", ":8080"), "listen address")
	base := flag.String("base", env("OPENRATE_BASE", "ZAR"), "default presentation base currency")
	refresh := flag.Duration("refresh", envDur("OPENRATE_REFRESH", time.Hour), "source refresh interval")
	fetchTimeout := flag.Duration("fetch-timeout", envDur("OPENRATE_FETCH_TIMEOUT", openrate.DefaultFetchTimeout), "per-source fetch timeout (the SARB host can be slow to connect)")
	srcSpec := flag.String("sources", env("OPENRATE_SOURCES", ""), "comma-separated FX sources (default: ecb,coinbase,luno,sarb; also: frankfurter,yahoo)")
	intSpec := flag.String("interest-sources", env("OPENRATE_INTEREST_SOURCES", ""), "comma-separated interest-rate sources (default: bis,sarbrates; fred auto-enables with key)")
	intRefresh := flag.Duration("interest-refresh", envDur("OPENRATE_INTEREST_REFRESH", 6*time.Hour), "interest-rate refresh interval")
	rpm := flag.Int("ratelimit", envInt("OPENRATE_RATELIMIT", 120), "per-IP API requests/minute (anti-scraping; 0 disables)")
	cors := flag.String("cors-origin", env("OPENRATE_CORS_ORIGIN", "*"), "Access-Control-Allow-Origin for the JSON API (default * — public read-only; set an origin to lock down)")
	trustedProxies := flag.String("trusted-proxies", env("OPENRATE_TRUSTED_PROXIES", ""), "comma-separated proxy IPs/CIDRs whose X-Forwarded-For is trusted for rate-limiting (default none → use RemoteAddr)")
	ui := flag.Bool("ui", envBool("OPENRATE_UI", true), "serve the embedded web console at / (-ui=false leaves only the JSON API; -tags noui removes it from the binary)")
	flag.Parse()

	log := slog.Default()

	// FromEnvSpec, not Build: the binary is where "an API key in .env turns its
	// source on" belongs. The library never reads the environment on its own.
	srcs := fxsource.FromEnvSpec(*srcSpec)
	if len(srcs) == 0 {
		log.Error("no valid sources configured", slog.String("spec", *srcSpec))
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	engine := openrate.NewEngine(openrate.EngineOptions{Base: *base, Logger: log})
	refresher := openrate.NewRefresher(engine, openrate.RefreshOptions{
		Sources:      srcs,
		Interval:     *refresh,
		FetchTimeout: *fetchTimeout,
		Logger:       log,
	})
	go func() { _ = refresher.Run(ctx) }()

	// Interest-rate engine: an independent store/snapshot (rates are flat
	// series, not a currency graph) served under /api/v1/interest/*. Policy
	// rates change slowly, so it refreshes on its own slower cadence.
	var extra []serve.Routable
	if intSrcs := ratesources.Build(*intSpec); len(intSrcs) > 0 {
		ist := ratestore.New(*intRefresh, log, intSrcs...)
		go ist.Run(ctx)
		extra = append(extra, interest.New(ist, *cors))
		log.Info("interest rates enabled", slog.Int("sources", len(intSrcs)), slog.Duration("refresh", *intRefresh))
	}

	var proxies []string
	if *trustedProxies != "" {
		proxies = strings.Split(*trustedProxies, ",")
	}
	api := serve.New(engine, serve.Options{
		UI:             *ui,
		CORSOrigin:     *cors,
		RateLimit:      *rpm,
		TrustedProxies: proxies,
		Status:         refresher.Status,
		Extra:          extra,
	})
	defer api.Close()

	srv := &http.Server{Addr: *addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("openrate listening",
			slog.String("addr", *addr), slog.String("base", *base),
			slog.Duration("refresh", *refresh), slog.Bool("ui", *ui))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	log.Info("openrate stopped")
}

// loadDotEnv reads a .env file (if present) and sets any KEY=VALUE pairs that
// aren't already in the environment. Dependency-free; real env vars win.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k != "" && os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
