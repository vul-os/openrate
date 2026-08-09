// Package openrate is an embeddable FX rate engine: a currency graph, an
// all-pairs snapshot materialized from it, and a grade attached to every number
// saying how much to trust it. It has no dependencies outside the standard
// library.
//
// The library is deliberately split so that a host program pays only for what
// it asks for:
//
//	fx        the pure core — graph, snapshot, accuracy model, Describe
//	fxsource  the Source interface, the adapters, and the only os.Getenv
//	openrate  Engine (computes) and Refresher (fetches) — this package
//	serve     the optional HTTP shell and the optional web console
//
// # Computing without fetching
//
// An Engine holds a snapshot and answers from it. Constructing one starts no
// goroutine, opens no socket, reads no environment variable and sends no
// packet:
//
//	e := openrate.NewEngine(openrate.EngineOptions{Base: "ZAR"})
//	c, err := e.Convert("USD", "ZAR", 100)   // ErrUnknownPair until it is fed
//
// It is safe to build one inside a server whose FX feature is switched off. It
// will do nothing, and it will say "I don't know" rather than return a zero.
//
// Feed it by hand — from a file, a cache, another process — with Load:
//
//	g := fx.NewGraph()
//	g.Replace("mysource", edges)
//	e.Load(g.Materialize(time.Now().UTC()))
//
// # Fetching, when you want it
//
// A Refresher is the only type here that touches the network, and it is
// constructed separately and explicitly:
//
//	r := openrate.NewRefresher(e, openrate.RefreshOptions{
//		Sources:  fxsource.Build("ecb,coinbase"),
//		Interval: time.Hour,
//	})
//	go r.Run(ctx)                      // or call r.Refresh(ctx) on your own schedule
//	if err := r.Ready(ctx); err != nil { ... }   // blocks until rates exist
//
// No Refresher means no packets, structurally — not by convention, and not by
// a flag that some other code path might flip.
//
// # Serving
//
// The JSON API and the web console live in the serve package and are entirely
// optional; see [github.com/vul-os/openrate/serve]. The openrate binary
// (cmd/openrate) is a thin main over exactly the three pieces above.
package openrate
