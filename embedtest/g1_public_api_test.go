// Package embedtest is openrate's embeddability suite, and it is deliberately
// a different module from openrate itself (see go.mod). Everything here uses
// only the published import paths — github.com/vul-os/openrate, .../fx,
// .../fxsource, .../serve, .../serve/web — because that is all a real host
// program can use.
//
// The guards it holds:
//
//	G1  the internal/ wall is real and the public API is sufficient (this file)
//	G2  constructing the library is inert: no goroutine, no packet, no getenv
//	G3  the pure core does not depend on the server or the console
//	G5  the same library works with the console compiled out (-tags noui)
//
// G7 (importing the library links no UI bytes) lives in hosts/ plus
// scripts/check-embed-linkage.sh, because it is a property of a linked binary
// rather than of a running test.
package embedtest

import (
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
)

// fixture is the instant every hand-built rate in this file is stamped with,
// and the clock every Engine here is given. Pinning both makes the age and the
// grade of a conversion exact rather than approximately-today.
var fixture = time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// TestG1EngineIsUsableFromAnotherModule is the whole point of this module: an
// outside caller builds a snapshot by hand, feeds it to an Engine, converts,
// and reads the provenance off the answer — with no Refresher, no server, and
// no access to anything under internal/.
//
// If openrate ever needs an unexported or internal type to do this, THIS FILE
// STOPS COMPILING. That is the guard. A compile failure here is not a broken
// test, it is the wall doing its job.
func TestG1EngineIsUsableFromAnotherModule(t *testing.T) {
	e := openrate.NewEngine(openrate.EngineOptions{
		Base:   "ZAR",
		Logger: quiet(),
		Now:    func() time.Time { return fixture },
	})

	// An engine nobody has fed says "I don't know" — it does not return a zero
	// and it does not go and find out.
	if _, err := e.Convert("USD", "ZAR", 100); !errors.Is(err, openrate.ErrUnknownPair) {
		t.Fatalf("Convert on an unfed engine: err = %v, want ErrUnknownPair", err)
	}
	if got := e.DefaultBase(); got != "ZAR" {
		t.Errorf("DefaultBase = %q, want ZAR", got)
	}

	// A snapshot built entirely by the host: no source, no network, no file.
	const usdZAR, eurUSD = 18.5, 1.1
	g := fx.NewGraph()
	g.Replace("handbuilt", []fx.Edge{
		{From: "USD", To: "ZAR", Rate: usdZAR, Source: "handbuilt", Time: fixture.Add(-30 * time.Second)},
		{From: "EUR", To: "USD", Rate: eurUSD, Source: "handbuilt", Time: fixture.Add(-30 * time.Second)},
	})
	e.Load(g.Materialize(fixture))

	// Case and surrounding space are the caller's business, not ours.
	c, err := e.Convert("  usd ", "zar", 100)
	if err != nil {
		t.Fatalf("Convert after Load: %v", err)
	}

	// The number.
	if c.Rate != usdZAR {
		t.Errorf("Rate = %v, want %v", c.Rate, usdZAR)
	}
	if want := 100 * usdZAR; c.Result != want {
		t.Errorf("Result = %v, want %v", c.Result, want)
	}
	if c.From != "USD" || c.To != "ZAR" || c.Amount != 100 {
		t.Errorf("Conversion identifies itself as %s->%s of %v", c.From, c.To, c.Amount)
	}

	// The provenance. This is the half an embedder cannot get from a plain
	// float, and the half that would quietly disappear if the facade ever
	// started re-deriving answers instead of passing them through.
	if c.Hops != 1 {
		t.Errorf("Hops = %d, want 1 for a directly quoted pair", c.Hops)
	}
	if len(c.Path) != 2 || c.Path[0] != "USD" || c.Path[1] != "ZAR" {
		t.Errorf("Path = %v, want [USD ZAR]", c.Path)
	}
	if len(c.Sources) != 1 || c.Sources[0] != "handbuilt" {
		t.Errorf("Sources = %v, want [handbuilt] — the host's own source name must survive", c.Sources)
	}
	if len(c.Legs) != 1 || c.Legs[0].Source != "handbuilt" || c.Legs[0].Rate != usdZAR {
		t.Errorf("Legs = %+v, want one leg of %v from handbuilt", c.Legs, usdZAR)
	}
	if c.AgeSec != 30 {
		t.Errorf("AgeSec = %v, want 30 (the engine's injected clock minus the edge's stamp)", c.AgeSec)
	}
	if !c.AsOf.Equal(fixture.Add(-30 * time.Second)) {
		t.Errorf("AsOf = %v, want the edge's timestamp", c.AsOf)
	}
	switch c.Quality.Grade {
	case "A", "B", "C", "D":
	default:
		t.Errorf("Quality.Grade = %q, want one of A B C D", c.Quality.Grade)
	}
	if c.Quality.Confidence <= 0 || c.Quality.Confidence > 1 {
		t.Errorf("Quality.Confidence = %v, want (0,1]", c.Quality.Confidence)
	}
	if c.Quality.Directness != "direct" {
		t.Errorf("Quality.Directness = %q, want direct", c.Quality.Directness)
	}

	// A triangulated pair, to prove the graph is really being traversed rather
	// than a quoted pair being echoed back.
	cross, err := e.Convert("EUR", "ZAR", 1)
	if err != nil {
		t.Fatalf("Convert EUR->ZAR: %v", err)
	}
	// Computed the same way the engine computes it — left to right, in float64
	// — because that product is exact bit for bit and a constant-folded
	// literal is not guaranteed to be the same double.
	a, b := eurUSD, usdZAR
	if want := a * b; cross.Rate != want {
		t.Errorf("EUR->ZAR rate = %v, want %v", cross.Rate, want)
	}
	if cross.Hops != 2 || len(cross.Path) != 3 || cross.Path[1] != "USD" {
		t.Errorf("EUR->ZAR went %v in %d hop(s), want a 2-hop cross through USD", cross.Path, cross.Hops)
	}

	// Rates(), the other read surface.
	m, err := e.Rates("")
	if err != nil {
		t.Fatalf("Rates(\"\"): %v", err)
	}
	if p, ok := m["USD"]; !ok || p.Rate != 1/usdZAR {
		t.Errorf("Rates(default base)[USD] = %+v (present=%v), want 1/%v", p, ok, usdZAR)
	}
	if _, err := e.Rates("XXX"); !errors.Is(err, openrate.ErrUnknownBase) {
		t.Errorf("Rates(\"XXX\"): err = %v, want ErrUnknownBase", err)
	}

	// Snapshot() is the escape hatch a host uses to cache or hand off the book.
	if snap := e.Snapshot(); snap == nil || len(snap.Currencies) != 3 {
		t.Errorf("Snapshot holds %v, want the three currencies just loaded", snap)
	}
}

// TestG1InternalPackagesStayUnreachable proves the wall the rest of this
// module stands outside of actually exists.
//
// Every other test here is evidence only if a compile failure was POSSIBLE. It
// wasn't, at first: this module was originally named
// github.com/vul-os/openrate/embedtest, the internal/ rule is applied to
// import paths rather than module boundaries, and so a blank import of
// github.com/vul-os/openrate/internal/redact compiled cleanly and the suite
// reported success. Nothing in the output distinguished that from a real pass.
//
// internalprobe/ is that import, kept behind a build tag. This test builds it
// and FAILS IF THE BUILD SUCCEEDS.
func TestG1InternalPackagesStayUnreachable(t *testing.T) {
	cmd := exec.Command("go", "build", "-tags", "internalprobe", "./internalprobe")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("embedtest was allowed to import github.com/vul-os/openrate/internal/redact.\n"+
			"The module boundary is not being enforced, so every other test in this module\n"+
			"proves nothing about the public API being sufficient. Check that go.mod's module\n"+
			"path is OUTSIDE github.com/vul-os/openrate/.\ngo build said: %s", out)
	}
	if !strings.Contains(string(out), "use of internal package") {
		t.Fatalf("the build failed, but not because of the internal/ rule — so this test is\n"+
			"measuring a compile error of some other kind and would keep passing after the\n"+
			"wall came down.\ngo build said: %s", out)
	}
	t.Logf("compiler refused as required: %s", strings.TrimSpace(string(out)))
}
