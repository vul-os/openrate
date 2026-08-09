// Command sidecar talks to `openrate serve` over HTTP, spawning and
// supervising the process itself so the operator never runs a server by hand.
//
// A Go program does not need this — see ../direct, which imports the engine and
// is strictly better in Go. It is here because the sidecar is the mode every
// other language shares, and because there are real reasons a Go host still
// picks it:
//
//   - One refresher for several processes. Four processes each importing
//     openrate and each running a Refresher is four times the load on ECB,
//     SARB and Coinbase, from four IPs, on four unsynchronised cadences.
//     One sidecar is one fetch.
//   - You want the rate limiter, the CORS policy and the trusted-proxy
//     handling that live in the HTTP shell, not the library.
//   - You want openrate restartable and upgradable independently of your
//     program.
//
// Two things this example makes a point of showing, because they are where a
// naive HTTP client gets it wrong:
//
//   - /healthz answers `ok` the instant the listener is up, BEFORE any source
//     has been fetched. It is a liveness probe, not a readiness one. Readiness
//     is "/api/v1/meta reports a non-empty currencies list", and that is what
//     waitReady polls for. (The library's equivalent is Refresher.Ready.)
//   - GET /api/v1/rates with an unknown base answers 200 with an empty book,
//     where the library returns ErrUnknownBase. That is the one deliberate
//     difference between the two surfaces, and a client that only checks the
//     status code will read "no rates" as success.
//
// Run it:
//
//	go build -o /tmp/openrate ./cmd/openrate
//	go run ./sdks/go/examples/sidecar -bin /tmp/openrate
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	bin := flag.String("bin", envOr("OPENRATE_BINARY", "openrate"),
		"path to the openrate binary (or a name on PATH)")
	sources := flag.String("sources", "ecb", "sources for the child to fetch")
	base := flag.String("base", "EUR", "default presentation base currency")
	flag.Parse()

	sc, err := startSidecar(*bin, *base, *sources)
	if err != nil {
		return err
	}
	// One defer covers every path below, including the error returns. Without
	// it a failed request leaks a serving openrate holding a port and a refresh
	// loop hitting ECB every hour forever.
	defer sc.Close()
	fmt.Println("sidecar:  ", sc.BaseURL, "pid", sc.cmd.Process.Pid)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// ----------------------------------------------------------------- ready
	// Liveness first — this returns almost immediately.
	fmt.Println("healthz:  ", sc.mustHealthz(ctx))
	// Then readiness, which is a different question with a different answer.
	meta, err := sc.waitReady(ctx, 45*time.Second)
	if err != nil {
		return err
	}
	fmt.Printf("meta:      base=%s currencies=%d built_at=%s\n",
		meta.DefaultBase, len(meta.Currencies), meta.BuiltAt)
	for _, s := range meta.Sources {
		if s.LastError != "" {
			fmt.Printf("  %-10s FAILED %s\n", s.Name, s.LastError)
		} else {
			fmt.Printf("  %-10s ok, %d edges\n", s.Name, s.Edges)
		}
	}

	// --------------------------------------------------------------- convert
	// The same JSON the C ABI's "convert" method returns, over a different
	// transport. One wire contract.
	conv, err := sc.convert(ctx, "EUR", "USD", 100)
	if err != nil {
		return fmt.Errorf("convert: %w", err)
	}
	fmt.Printf("convert:   %.2f %s = %.4f %s (rate %.6f, %d hop(s) via %v, grade %s)\n",
		conv.Amount, conv.From, conv.Result, conv.To,
		conv.Rate.Rate, conv.Rate.Hops, conv.Rate.Path, conv.Rate.Quality.Grade)

	// ----------------------------------------------------------------- rates
	rates, err := sc.rates(ctx, *base)
	if err != nil {
		return fmt.Errorf("rates: %w", err)
	}
	fmt.Printf("rates(%s): %d pairs, built_at %s\n", *base, len(rates.Rates), rates.BuiltAt)

	// The deliberate difference, demonstrated rather than described: an unknown
	// base is an ERROR in the library and a 200 with an empty book here.
	bogus, err := sc.rates(ctx, "XXX")
	if err != nil {
		return fmt.Errorf("rates(XXX): %w", err)
	}
	fmt.Printf("rates(XXX): HTTP 200 with %d pairs — the library would have "+
		"returned ErrUnknownBase. Check the book, not the status code.\n", len(bogus.Rates))

	return nil
}

// ---------------------------------------------------------------------------
// Wire types — a subset of what openrate publishes, which is the point of a
// documented JSON contract: bind the fields you use.
// ---------------------------------------------------------------------------

type sourceStatus struct {
	Name      string `json:"name"`
	Edges     int    `json:"edges"`
	LastOK    string `json:"last_ok"`
	LastError string `json:"last_error,omitempty"`
}

type metaResponse struct {
	DefaultBase string         `json:"default_base"`
	BuiltAt     string         `json:"built_at"`
	Currencies  []string       `json:"currencies"`
	Sources     []sourceStatus `json:"sources"`
}

// conversion is GET /api/v1/convert.
//
// Note the shape, because it is NOT the Go type. openrate.Engine.Convert
// returns a FLAT fx.Conversion with .Rate, .Hops, .Path and .Quality on the
// top level. The HTTP endpoint nests all of that under a "rate" object:
//
//	in-process   c.Rate,       c.Hops,       c.Quality.Grade
//	over HTTP    .rate.rate,   .rate.hops,   .rate.quality.grade
//
// A binding that assumes `rate` is a number — which the field name invites —
// fails to decode. This example got that wrong on the first run.
type conversion struct {
	From   string   `json:"from"`
	To     string   `json:"to"`
	Amount float64  `json:"amount"`
	Result float64  `json:"result"`
	Rate   rateView `json:"rate"`
}

// rateView is the provenance object openrate nests under "rate", and the same
// object that appears as each value of the "rates" map.
type rateView struct {
	Rate    float64  `json:"rate"`
	Hops    int      `json:"hops"`
	AsOf    string   `json:"as_of"`
	AgeSec  float64  `json:"age_sec"`
	Path    []string `json:"path"`
	Sources []string `json:"sources"`
	Quality struct {
		Grade      string  `json:"grade"`
		Confidence float64 `json:"confidence"`
		Freshness  string  `json:"freshness"`
		Directness string  `json:"directness"`
	} `json:"quality"`
}

type ratesResponse struct {
	Base    string              `json:"base"`
	BuiltAt string              `json:"built_at"`
	Rates   map[string]rateView `json:"rates"`
}

// ---------------------------------------------------------------------------
// The supervised child process
// ---------------------------------------------------------------------------

type sidecar struct {
	BaseURL string
	cmd     *exec.Cmd
	client  *http.Client
}

// startSidecar picks a free port, spawns the binary on it, and waits for the
// listener. On any failure it kills whatever it started, so a caller never has
// to clean up after a constructor that returned an error.
func startSidecar(bin, base, sources string) (*sidecar, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// -ui=false leaves only the JSON API. The embedded console is a nice thing
	// to serve to humans and dead weight in a supervised sidecar.
	cmd := exec.Command(bin, "-addr", addr, "-base", base, "-sources", sources, "-ui=false")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn %s: %w", bin, err)
	}

	sc := &sidecar{
		BaseURL: "http://" + addr,
		cmd:     cmd,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
	if err := sc.waitLive(10 * time.Second); err != nil {
		sc.Close()
		return nil, err
	}
	return sc, nil
}

// Close stops the child and reaps it. Idempotent.
func (s *sidecar) Close() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	// SIGTERM, because openrate installs a signal handler and shuts the
	// listener down cleanly; Kill would be fine too but would skip that.
	_ = s.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _, _ = s.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
	s.cmd = nil
}

// waitLive polls /healthz — liveness only. It says nothing about rates.
func (s *sidecar) waitLive(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	var last error = errors.New("connection refused")
	for time.Now().Before(deadline) {
		if s.cmd.ProcessState != nil {
			return fmt.Errorf("openrate exited before listening: %s", s.cmd.ProcessState)
		}
		resp, err := client.Get(s.BaseURL + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Errorf("healthz returned %s", resp.Status)
		} else {
			last = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("openrate did not listen within %s: %w", timeout, last)
}

// waitReady polls /api/v1/meta until the engine holds at least one currency,
// and returns an ERROR if it never does.
//
// This is the HTTP equivalent of Refresher.Ready, and it is a different
// question from /healthz — which answers ok the instant the listener binds,
// before any source has been fetched. Returning an error rather than an empty
// result is the point: a caller that treats /healthz as readiness gets
// "unknown or unreachable currency pair" from every conversion and exits 0,
// because that message reads like a bad currency code rather than like "this
// server has no rates yet".
//
// The delay backs off from 100ms to a 2s ceiling. A fixed 200ms poll is 300
// requests a minute, and `openrate serve` ships an anti-scraping rate limiter
// at 120/minute per IP, so a long wait gets itself a 429 from the very server
// it is waiting for. (The Rust SDK's first version did exactly that and failed
// on its first run.) With backoff a 45-second wait is about 25 requests.
func (s *sidecar) waitReady(ctx context.Context, timeout time.Duration) (*metaResponse, error) {
	deadline := time.Now().Add(timeout)
	var last metaResponse
	delay := 100 * time.Millisecond
	const maxDelay = 2 * time.Second
	for time.Now().Before(deadline) {
		var m metaResponse
		if err := s.getJSON(ctx, "/api/v1/meta", &m); err != nil {
			return nil, fmt.Errorf("meta: %w", err)
		}
		if len(m.Currencies) > 0 {
			return &m, nil
		}
		last = m
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
			if delay *= 2; delay > maxDelay {
				delay = maxDelay
			}
		}
	}
	// Report the source errors rather than just "timed out": the reason is
	// almost always one named source failing, and meta already knows which.
	var why []string
	for _, src := range last.Sources {
		if src.LastError != "" {
			why = append(why, src.Name+": "+src.LastError)
		}
	}
	if len(why) > 0 {
		return nil, fmt.Errorf("no rates within %s (%s)", timeout, strings.Join(why, "; "))
	}
	return nil, fmt.Errorf("no rates within %s", timeout)
}

func (s *sidecar) mustHealthz(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/healthz", nil)
	if err != nil {
		return "error: " + err.Error()
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return "error: " + err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return fmt.Sprintf("%s %q (liveness only — no rates implied)", resp.Status, strings.TrimSpace(string(body)))
}

func (s *sidecar) convert(ctx context.Context, from, to string, amount float64) (*conversion, error) {
	q := url.Values{}
	q.Set("from", from)
	q.Set("to", to)
	q.Set("amount", fmt.Sprintf("%g", amount))
	var c conversion
	if err := s.getJSON(ctx, "/api/v1/convert?"+q.Encode(), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *sidecar) rates(ctx context.Context, base string) (*ratesResponse, error) {
	var r ratesResponse
	if err := s.getJSON(ctx, "/api/v1/rates?base="+url.QueryEscape(base), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *sidecar) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: %s", resp.Status, path, strings.TrimSpace(string(data)))
	}
	// openrate's writeJSON sets a 200 header and then encodes, so a mid-body
	// encoding failure yields a successful status with a truncated body. Decode
	// errors are therefore real and must not be swallowed.
	return json.Unmarshal(data, out)
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
