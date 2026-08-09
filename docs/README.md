# openrate documentation

Everything you need to run, consume, embed, and extend openrate.

New here? [Quickstart](quickstart.md) has one starting point per audience, and
[Which mode should I choose](deployment-modes.md) is a page long and settles the
library-vs-sidecar question before you write any code.

## Start here

| Guide | What's inside |
|---|---|
| [Quickstart](quickstart.md) | Five starting points: see it work, run it as a service, embed it in Go, call it from another language, or bring your own rates |
| [Which mode should I choose](deployment-modes.md) | Library, CLI, sidecar or C ABI — the decision, with measured cost and size for each |
| [Troubleshooting](troubleshooting.md) | Symptoms in the order they happen, and what each one actually means |

## Embedding

| Guide | What's inside |
|---|---|
| [Embed as a Go library](library.md) | `Engine`, `Refresher`, `fx`, `fxsource`, `serve`, the `noui` tag — the full reference |
| [Proving it sends nothing](zero-network.md) | The headline property: an `Engine` constructed with the feature off sends zero packets, counted with a control |
| [Use it from another language](c-abi.md) | The C ABI, its honest costs, and why the sidecar is usually the better answer |

## Running the server

| Guide | What's inside |
|---|---|
| [Configuration](configuration.md) | Flags, environment variables, and the source spec |
| [API reference](api.md) | Every endpoint, query params, and full response shapes |
| [Web UI](web-ui.md) | The embedded, dependency-free HTML console (converter + rates board) |

## The numbers

| Guide | What's inside |
|---|---|
| [The graph model](graph-model.md) | Why openrate models currencies as a graph, not a base |
| [Accuracy & quality](../ACCURACY.md) | How grades, confidence, corroboration and caveats are computed |
| [Sources](../SOURCES.md) | Full source catalog, cadence, and freshness notes |
| [Interest rates](interest-rates.md) | The optional policy/reference-rate engine under `/api/v1/interest/*` — `internal/` and serve-only |

## Elsewhere in the repository

| Document | Purpose |
|---|---|
| [`ffi/README.md`](../ffi/README.md) | The C ABI reference: every function, the memory rules, the measured numbers |
| [`SECURITY.md`](../SECURITY.md) | Disclosure policy |
| [`CHANGELOG.md`](../CHANGELOG.md) | Release history |
| [`ROADMAP.md`](../ROADMAP.md) | What is planned, and what is deliberately not |
| [License](../LICENSE-MIT) OR [Apache-2.0](../LICENSE-APACHE) | Dual-licensed MIT OR Apache-2.0 |
