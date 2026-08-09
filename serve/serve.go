// Package serve is openrate's HTTP shell: the JSON API, the optional web
// console, and the hardening the public deployment runs with.
//
// It is a shell in the literal sense — everything it answers with comes from an
// [Engine] it does not own and cannot refresh. Nothing here fetches, and the
// package is entirely optional: a Go program that wants rates in process should
// use the openrate library directly and never import this.
//
// The split matters for what it makes impossible. serve cannot start a refresh
// loop, because it holds no sources; it cannot reach the network at all. A
// deployment that wants both constructs an Engine, a Refresher and a Server,
// and can see all three in one place.
package serve

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/fxsource"
	"github.com/vul-os/openrate/serve/ratelimit"
	"github.com/vul-os/openrate/serve/web"
)

// Engine is the read side serve needs: a presentation default and a snapshot to
// answer from. *openrate.Engine satisfies it, and so can a host's own type —
// serving a snapshot from a cache or a file needs nothing else.
//
// It is deliberately this small. Anything wider would let the HTTP layer drive
// the engine rather than read it.
type Engine interface {
	// DefaultBase is the presentation base used when a request names none.
	DefaultBase() string
	// Snapshot returns the immutable view to answer from. It must never be nil.
	Snapshot() *fx.Snapshot
}

// Routable is anything that mounts its own routes on the shared mux — the
// interest-rate API (serve/interest) is one, and a host can add its own.
type Routable interface {
	Routes(mux *http.ServeMux)
}

// Options configures a Server. The zero value is a valid JSON-only server with
// no rate limiting, no console and no source status.
type Options struct {
	// UI mounts the embedded console at "/". The binary sets it; an embedder
	// usually wants only the JSON API, which is why it is off by default.
	//
	// In a build tagged `noui` the console does not exist and this mounts a
	// small JSON stub in its place (see serve/web).
	UI bool
	// CORSOrigin is the Access-Control-Allow-Origin sent on JSON responses.
	// Empty means "*" — the API is public and read-only.
	CORSOrigin string
	// RateLimit is per-IP API requests/minute; 0 disables it. The limiter runs
	// a background sweeper, which starts when Handler is first called and stops
	// on Close.
	RateLimit int
	// TrustedProxies lists proxy IPs/CIDRs whose X-Forwarded-For is honoured
	// for rate limiting. Empty never trusts XFF and uses RemoteAddr.
	TrustedProxies []string
	// Status supplies the per-source fetch outcomes for /api/v1/meta — pass a
	// Refresher's Status method. nil reports no sources, which is the truth for
	// a server over an engine nobody is refreshing.
	Status func() []fxsource.Status
	// Extra mounts additional route sets on the same mux.
	Extra []Routable
	// Now is the clock used to age and grade rates in responses. nil means
	// time.Now. A test can pin it and diff the whole response body.
	Now func() time.Time
}

// Server answers openrate's HTTP API over an Engine.
type Server struct {
	engine Engine
	opts   Options
	now    func() time.Time
	cors   string

	limiterOnce sync.Once
	limiter     *ratelimit.Limiter
}

// New returns a Server. It starts nothing, binds nothing and fetches nothing —
// building the handler and listening are the caller's next two steps.
func New(e Engine, opts Options) *Server {
	cors := opts.CORSOrigin
	if cors == "" {
		cors = "*"
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Server{engine: e, opts: opts, now: now, cors: cors}
}

// Routes registers the API (and, if enabled, the console) on mux. Use it to
// mount openrate inside a larger application's own mux; use Handler for the
// standalone server with openrate's hardening applied.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/rates", s.handleRates)
	mux.HandleFunc("/api/v1/convert", s.handleConvert)
	mux.HandleFunc("/api/v1/meta", s.handleMeta)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/readyz", s.handleReady)
	for _, r := range s.opts.Extra {
		if r != nil {
			r.Routes(mux)
		}
	}
	if s.opts.UI {
		mux.Handle("/", web.Handler())
	}
}

// Handler returns the full standalone handler: the routes plus openrate's
// hardening — a restrictive robots.txt, security headers, no-store caching on
// the API, and per-IP rate limiting on /api/ paths when configured. The console
// and its assets are not rate-limited.
//
// The rate limiter's background sweeper starts here rather than in New, so that
// constructing a Server remains free of goroutines. Call Close to stop it.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.Routes(mux)

	if s.opts.RateLimit > 0 {
		s.limiterOnce.Do(func() {
			s.limiter = ratelimit.New(s.opts.RateLimit, s.opts.RateLimit/2+1, s.opts.TrustedProxies...)
		})
	}
	return s.guard(mux)
}

// Close releases what Handler started (currently the rate limiter's sweeper).
// It is safe to call on a Server whose Handler was never built, and safe to
// call more than once.
func (s *Server) Close() error {
	if s.limiter != nil {
		s.limiter.Stop()
	}
	return nil
}

func (s *Server) guard(mux http.Handler) http.Handler {
	limiter := s.limiter
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
