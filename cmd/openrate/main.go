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
	"syscall"
	"time"

	"github.com/vul-os/openrate/internal/api"
	"github.com/vul-os/openrate/internal/sources"
	"github.com/vul-os/openrate/internal/store"
	"github.com/vul-os/openrate/web"
)

func main() {
	addr := flag.String("addr", env("OPENRATE_ADDR", ":8080"), "listen address")
	base := flag.String("base", env("OPENRATE_BASE", "ZAR"), "default presentation base currency")
	refresh := flag.Duration("refresh", envDur("OPENRATE_REFRESH", time.Hour), "source refresh interval")
	flag.Parse()

	st := store.New(*refresh, sources.NewECB(), sources.NewSARB())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go st.Run(ctx)

	mux := http.NewServeMux()
	api.New(st, *base).Routes(mux)

	if sub, err := web.FS(); err == nil {
		mux.Handle("/", http.FileServer(http.FS(sub)))
	} else {
		log.Printf("web ui unavailable: %v", err)
	}

	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
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

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
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
