# The graph model

Most rate APIs pick one base currency (usually EUR or USD) and derive everything
through it. openrate doesn't. It keeps each source's quotes in their **native
base** — ECB in EUR, SARB in ZAR, Coinbase per-pair — as edges in a **currency
graph**, and computes any pair as the product of rates along the shortest path
between the two currencies.

## Why a graph

- **ZAR is the anchor for free.** It's just the default presentation base — a view
  over the same graph. Ask for `?base=ZAR`, `?base=USD`, or anything else; nothing
  is privileged in storage.
- **Directly quoted pairs win.** A breadth-first search reaches a pair by the
  fewest hops first, so a direct quote (`hops: 1`) always beats a triangulated
  cross (`hops: 2+`).
- **No single point of contamination.** A bad edge only affects paths that go
  through it — not every pair in the system, as it would with a single-base model.
- **Provenance on every number.** Each rate carries the `path`, the `legs` (each
  hop's actual rate and source), `hops`, `as_of`, and `age_sec`, so consumers see
  exactly how the number was produced and how stale it is.

## How a rate is computed

1. Every source contributes **edges** — a quote is a directed rate between two
   currencies, stamped with its source and time.
2. To resolve `FROM → TO`, openrate runs **BFS** from `FROM`, so the first time it
   reaches `TO` it has done so over the fewest edges.
3. The pair's `rate` is the product of the edge rates along that path; `legs`
   records each hop, and `sources` lists the distinct sources involved.
4. `as_of` is the freshest edge timestamp on the path, and `age_sec` is how long
   ago that was — which matters because fiat quotes freeze on weekends.

## Freshest-direct-edge selection, by example

Because BFS prefers the fewest hops and openrate keeps the freshest edge per pair:

- `USD → ZAR` resolves to the **live Coinbase** quote (seconds old, direct).
- `EUR/GBP/JPY → ZAR` resolve to **SARB's authoritative** direct quotes.

Each is chosen automatically by the graph — there is no per-currency
special-casing in the routing.

## Trusting the number

Path selection decides *which* number you get; the **quality** assessment tells
you *how much to trust it* — combining freshness, directness, source authority,
cross-source agreement, and per-currency caveats into a grade (A–D) and
confidence score. See [Accuracy & quality](../ACCURACY.md).

## Adding a source

Implement the `sources.Source` interface and register it in
`internal/sources/registry.go`. Once it emits edges, the graph picks them up
automatically — a fresher or more direct quote will start winning paths with no
other changes. See [SOURCES.md](../SOURCES.md).
