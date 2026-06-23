# Embed as a Go library

The `cmd/openrate` binary keeps its building blocks under `internal/`, so they
can't be imported directly. The **root package** (`github.com/vul-os/openrate`) is
the supported public surface: it runs the same store, sources, API, and hardening
the binary does, but in-process — no subprocess, no port juggling.

```go
import "github.com/vul-os/openrate"

local, err := openrate.Start(openrate.Options{}) // ZAR base, hourly refresh, ephemeral port
if err != nil {
	log.Fatal(err)
}
defer local.Close()

resp, _ := http.Get(local.APIBaseURL() + "/rates") // or local.BaseURL + "/healthz"
```

`Start` builds the engine, launches it in a background goroutine, and **returns
once `/healthz` is serving** — so the gateway is ready the moment `Start` returns.

## `Options`

The zero value is valid and mirrors the binary's defaults.

| Field | Type | Default | Description |
|---|---|---|---|
| `Addr` | `string` | ephemeral `127.0.0.1` port | Listen address |
| `Base` | `string` | `"ZAR"` | Default presentation base currency |
| `Refresh` | `time.Duration` | `1h` | Source refresh interval |
| `Sources` | `string` | default set | Comma-separated source spec (see [Configuration](configuration.md)) |
| `RateLimit` | `int` | `0` (disabled) | Per-IP API requests/minute |
| `ServeUI` | `bool` | `false` | Mount the embedded React UI at `/` |
| `ReadyTimeout` | `time.Duration` | `10s` | How long `Start` waits for `/healthz` |

> Rate limiting and the UI default **off** for embedded use — most embedders want
> only the JSON API. Turn them on explicitly if you want parity with the binary.

## `Local`

| Member | Description |
|---|---|
| `BaseURL` | `http://host:port` root (no trailing slash) |
| `APIBaseURL()` | `BaseURL + "/api/v1"` — the JSON API root |
| `Close() error` | Stops refreshing, gracefully shuts the server down (5s timeout), and waits for it to stop. Idempotent. |

## Notes

- **Provider keys** for paid sources are read from the environment, exactly as the
  binary reads them (see [Configuration](configuration.md)).
- **Errors:** `Start` returns an error if no valid sources are configured, if the
  listen address can't be bound, or if `/healthz` doesn't come up within
  `ReadyTimeout`.
- Importing this package pulls in the whole module (the `internal/` engine plus
  the embedded UI assets) — unavoidable for true in-process embedding.

## Related

- [API reference](api.md) — what you'll be calling on `APIBaseURL()`
- [Configuration](configuration.md) — the same options, as binary flags
