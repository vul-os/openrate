# openrate documentation

Everything you need to run, consume, embed, and extend openrate.

## Guides

| Guide | What's inside |
|---|---|
| [API reference](api.md) | Every endpoint, query params, and full response shapes |
| [Configuration](configuration.md) | Flags, environment variables, and the source spec |
| [Embed as a Go library](library.md) | Import `Engine`/`Refresher` directly — no subprocess, no HTTP, opt in to fetching and serving separately |
| [The graph model](graph-model.md) | Why openrate models currencies as a graph, not a base |
| [Interest rates](interest-rates.md) | The optional policy/reference-rate engine under `/api/v1/interest/*` |
| [Accuracy & quality](../ACCURACY.md) | The grade/confidence model behind every rate |
| [Sources](../SOURCES.md) | Full source catalog, cadence, and freshness notes |
| [Web UI](web-ui.md) | The embedded, dependency-free HTML UI (converter + rates board) |

## Reference

| Document | Purpose |
|---|---|
| [Accuracy model](../ACCURACY.md) | How grades, confidence, and caveats are computed |
| [Source catalog](../SOURCES.md) | Per-source detail and provenance |
| [License](../LICENSE-MIT) OR [Apache-2.0](../LICENSE-APACHE) | Dual-licensed MIT OR Apache-2.0 |

## Quick links

- Run it: [`configuration.md`](configuration.md)
- Consume the API: [`api.md`](api.md)
- Embed it in Go: [`library.md`](library.md)
- Understand the numbers: [`graph-model.md`](graph-model.md) + [`../ACCURACY.md`](../ACCURACY.md)
