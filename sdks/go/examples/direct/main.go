// Command direct uses openrate in-process — a plain package import, no shared
// library, no server, no port.
//
// The example is built around the property that is openrate's whole design:
//
//	ENGINE     computes. openrate.NewEngine starts no goroutine, opens no
//	           socket, reads no environment variable and sends no packet.
//	           Ever. It answers from the snapshot it holds.
//
//	REFRESHER  fetches. openrate.NewRefresher is a SEPARATE construction with
//	           its own lifetime, and it is the only type in openrate that can
//	           open a socket.
//
// So this program runs in two phases, and the first one is the headline. Phase
// one builds an engine, feeds it rates from a literal in this file, and answers
// questions — with no network, provably, because there is no code path from an
// Engine to a socket. Phase two is opt-in behind -refresh and is the only part
// that talks to the internet.
//
// A host that never calls NewRefresher cannot acquire an outbound dependency by
// accident. That is not a convention to be careful about; it is the absence of
// a code path.
//
// Run it:
//
//	go run ./sdks/go/examples/direct              # zero packets
//	go run ./sdks/go/examples/direct -refresh     # fetch from ECB, then answer
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/fxsource"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	doRefresh := flag.Bool("refresh", false,
		"phase two: build a Refresher and fetch from live sources. THIS OPENS SOCKETS.")
	sources := flag.String("sources", "ecb", "comma-separated sources for -refresh")
	base := flag.String("base", "ZAR", "default presentation base currency")
	flag.Parse()

	// A quiet logger, so the example's own output is the only output. nil would
	// mean slog.Default(), which lands on this process's stderr.
	quiet := slog.New(slog.DiscardHandler)

	// ------------------------------------------------------------------------
	// Phase one: the engine, alone. Zero packets.
	// ------------------------------------------------------------------------
	eng := openrate.NewEngine(openrate.EngineOptions{Base: *base, Logger: quiet})

	// An engine with no snapshot honestly says it does not know, rather than
	// guessing or fetching. This is what a feature flag that is off looks like.
	if _, err := eng.Convert("USD", "ZAR", 1); !errors.Is(err, openrate.ErrUnknownPair) {
		return fmt.Errorf("expected ErrUnknownPair from an unloaded engine, got %v", err)
	}
	fmt.Println("empty engine:  USD->ZAR is ErrUnknownPair (it did not go and look)")

	// Load rates you obtained yourself: from a cache, a file, a colleague, a
	// vendor feed, a test fixture. The engine does not care where they came
	// from, and this path is why openrate is usable in a process that is not
	// allowed to make outbound calls at all.
	asOf := time.Date(2026, 8, 8, 16, 0, 0, 0, time.UTC)
	g := fx.NewGraph()
	g.Replace("desk", []fx.Edge{
		{From: "USD", To: "ZAR", Rate: 18.42, Source: "desk", Time: asOf},
		{From: "EUR", To: "USD", Rate: 1.0865, Source: "desk", Time: asOf},
		{From: "GBP", To: "USD", Rate: 1.2740, Source: "desk", Time: asOf},
	})
	eng.Load(g.Materialize(asOf))
	fmt.Printf("loaded:        %d currencies, built_at %s\n",
		len(eng.Snapshot().Currencies), eng.Snapshot().BuiltAt.Format(time.RFC3339))

	// A direct pair.
	if err := show(eng, "USD", "ZAR", 100); err != nil {
		return err
	}
	// A triangulated pair: EUR->ZAR exists only as EUR->USD->ZAR. openrate finds
	// it and tells you it did — Hops and Path are part of the answer, not a
	// detail you have to reconstruct.
	if err := show(eng, "EUR", "ZAR", 100); err != nil {
		return err
	}
	// And it still refuses what it genuinely cannot reach.
	if _, err := eng.Convert("JPY", "ZAR", 1); err == nil {
		return errors.New("expected an error for the unreachable pair JPY->ZAR")
	} else {
		fmt.Printf("JPY->ZAR:      %v\n", err)
	}

	// Rates: every known currency against one base.
	rates, err := eng.Rates(*base)
	if err != nil {
		return fmt.Errorf("rates: %w", err)
	}
	keys := make([]string, 0, len(rates))
	for k := range rates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("rates(%s):    ", *base)
	for _, k := range keys {
		fmt.Printf(" %s=%.6f", k, rates[k].Rate)
	}
	fmt.Println()

	if !*doRefresh {
		fmt.Println()
		fmt.Println("no Refresher was constructed, so this process opened no socket.")
		fmt.Println("pass -refresh for phase two.")
		return nil
	}

	// ------------------------------------------------------------------------
	// Phase two: the refresher. This is the part that touches the network.
	// ------------------------------------------------------------------------
	fmt.Println()
	srcs := fxsource.Build(*sources)
	if len(srcs) == 0 {
		return fmt.Errorf("no known sources in %q", *sources)
	}
	ref := openrate.NewRefresher(eng, openrate.RefreshOptions{
		Sources:      srcs,
		Logger:       quiet,
		FetchTimeout: 20 * time.Second,
	})
	// Constructing it STILL sends nothing. Fetching starts on the next line.
	fmt.Printf("refresher:     %d source(s), no packet sent yet\n", len(srcs))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Refresh replaces each source's contribution atomically. The engine keeps
	// serving the previous snapshot until the new one is materialized, so a
	// slow or failing source never leaves a reader with no rates.
	if err := ref.Refresh(ctx); err != nil {
		// A partial failure is reported per source, below, and is not fatal.
		fmt.Printf("refresh:       %v\n", err)
	}
	for _, st := range ref.Status() {
		switch {
		case st.LastError != "":
			fmt.Printf("  %-10s FAILED %s\n", st.Name, st.LastError)
		default:
			fmt.Printf("  %-10s ok, %d edges, last_ok %s\n",
				st.Name, st.Edges, st.LastOK.Format(time.RFC3339))
		}
	}
	fmt.Printf("engine now:    %d currencies\n", len(eng.Snapshot().Currencies))
	if err := show(eng, "EUR", "USD", 100); err != nil {
		return err
	}

	// Run(ctx) would loop on RefreshOptions.Interval instead; cancelling ctx
	// stops it. There is no Close: a Refresher owns no handle, only a goroutine
	// you started, and the context you gave it is how you stop that.
	return nil
}

// show prints one conversion with the provenance openrate attaches to it. That
// provenance — the path, the sources, the age, the grade — is what an
// in-process caller gets for free and a JSON consumer has to re-parse.
func show(eng *openrate.Engine, from, to string, amount float64) error {
	c, err := eng.Convert(from, to, amount)
	if err != nil {
		return fmt.Errorf("convert %s->%s: %w", from, to, err)
	}
	fmt.Printf("%s->%s:      %.2f %s = %.4f %s (rate %.6f, %d hop(s) via %v, grade %s, %v old)\n",
		from, to, amount, from, c.Result, to,
		c.Rate, c.Hops, c.Path, c.Quality.Grade,
		time.Duration(c.AgeSec)*time.Second)
	return nil
}
