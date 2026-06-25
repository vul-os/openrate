// Command openrate runs the rate engine: it ingests open central-bank/venue
// sources into a currency graph, materializes an all-pairs snapshot anchored on
// ZAR, and serves a JSON API plus an embedded React UI from a single binary.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vul-os/openrate/internal/api"
	"github.com/vul-os/openrate/internal/ratelimit"
	"github.com/vul-os/openrate/internal/ratesapi"
	"github.com/vul-os/openrate/internal/ratesources"
	"github.com/vul-os/openrate/internal/ratestore"
	"github.com/vul-os/openrate/internal/sources"
	"github.com/vul-os/openrate/internal/store"
	"github.com/vul-os/openrate/web"
)

func main() {
	loadDotEnv(".env")
	addr := flag.String("addr", env("OPENRATE_ADDR", ":8080"), "listen address")
	base := flag.String("base", env("OPENRATE_BASE", "ZAR"), "default presentation base currency")
	refresh := flag.Duration("refresh", envDur("OPENRATE_REFRESH", time.Hour), "source refresh interval")
	srcSpec := flag.String("sources", env("OPENRATE_SOURCES", ""), "comma-separated FX sources (default: ecb,coinbase,luno,sarb; also: frankfurter,yahoo)")
	intSpec := flag.String("interest-sources", env("OPENRATE_INTEREST_SOURCES", ""), "comma-separated interest-rate sources (default: bis,sarbrates; fred auto-enables with key)")
	intRefresh := flag.Duration("interest-refresh", envDur("OPENRATE_INTEREST_REFRESH", 6*time.Hour), "interest-rate refresh interval")
	rpm := flag.Int("ratelimit", envInt("OPENRATE_RATELIMIT", 120), "per-IP API requests/minute (anti-scraping; 0 disables)")
	flag.Parse()

	srcs := sources.Build(*srcSpec)
	if len(srcs) == 0 {
		log.Fatal("no valid sources configured")
	}
	st := store.New(*refresh, srcs...)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go st.Run(ctx)

	mux := http.NewServeMux()
	api.New(st, *base).Routes(mux)

	// Interest-rate engine: an independent store/snapshot (rates are flat series,
	// not a currency graph) served under /api/v1/interest/*. Policy rates change
	// slowly, so it refreshes on its own slower cadence.
	if intSrcs := ratesources.Build(*intSpec); len(intSrcs) > 0 {
		ist := ratestore.New(*intRefresh, intSrcs...)
		go ist.Run(ctx)
		ratesapi.New(ist).Routes(mux)
		log.Printf("interest rates: %d source(s), refresh %s", len(intSrcs), *intRefresh)
	}

	if sub, err := web.FS(); err == nil {
		mux.Handle("/", http.FileServer(http.FS(sub)))
	} else {
		log.Printf("web ui unavailable: %v", err)
	}

	var limiter *ratelimit.Limiter
	if *rpm > 0 {
		limiter = ratelimit.New(*rpm, *rpm/2+1)
	}
	srv := &http.Server{Addr: *addr, Handler: guard(mux, limiter), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("openrate listening on %s (base=%s, refresh=%s)", *addr, *base, *refresh)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	log.Println("openrate stopped")
}

// guard wraps the mux with anti-scraping + hardening: a restrictive robots.txt,
// security headers, no-store caching on the API, and per-IP rate limiting on
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
