# Web UI

openrate ships a Vite + React UI that is **embedded into the binary** via
`go:embed` and served from `/` — no separate service to deploy. It includes a
converter (with the rate's quality grade), a rates browser, and a dedicated
**Accuracy** page documenting the methodology.

## Develop

```bash
npm --prefix web install
npm --prefix web run dev      # Vite dev server, proxies /api to :8080
```

Run the Go server (`go run ./cmd/openrate`) alongside the dev server; the Vite
proxy forwards `/api` calls to it.

## Build (regenerate the embedded assets)

```bash
npm --prefix web run build    # regenerates web/dist
```

`web/dist` is what `go:embed` bakes into the binary, so rebuild it before
`go build` whenever the UI changes.

## Serving from the binary

The compiled binary serves the UI at `/` automatically. When [embedding openrate
as a library](library.md), the UI is **off by default** — set
`Options{ServeUI: true}` to mount it.

## Related

- [API reference](api.md) — the endpoints the UI calls
- [Accuracy & quality](../ACCURACY.md) — what the Accuracy page documents
