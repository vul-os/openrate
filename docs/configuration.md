# Configuration

openrate is configured with command-line flags, each backed by an environment
variable. Real environment variables and flags both win over a `.env` file.

## Flags & environment variables

| Flag | Env var | Default | Description |
|---|---|---|---|
| `-addr` | `OPENRATE_ADDR` | `:8080` | Listen address |
| `-base` | `OPENRATE_BASE` | `ZAR` | Default presentation base currency |
| `-refresh` | `OPENRATE_REFRESH` | `1h` | Source refresh interval (Go duration, e.g. `30m`) |
| `-sources` | `OPENRATE_SOURCES` | `ecb,coinbase,luno,sarb` | Comma-separated source spec |
| `-ratelimit` | `OPENRATE_RATELIMIT` | `120` | Per-IP API requests/minute (anti-scraping; `0` disables) |

```bash
# flags
./openrate -addr :8080 -base ZAR -refresh 1h -sources ecb,coinbase,luno,sarb

# or environment
OPENRATE_ADDR=:8080 OPENRATE_BASE=ZAR OPENRATE_REFRESH=1h ./openrate
```

## `.env` file

If a `.env` file is present in the working directory, openrate loads any
`KEY=VALUE` pairs that aren't already set in the environment (dependency-free; real
env vars take precedence). Lines beginning with `#` are ignored.

## The source spec

`-sources` / `OPENRATE_SOURCES` is a comma-separated list of source keys. If it
resolves to **no valid sources**, the binary exits with an error rather than
serving empty data.

| Key | Default | Notes |
|---|---|---|
| `ecb` | ✅ | ECB daily reference file |
| `coinbase` | ✅ | Free, no-auth fiat + crypto (best open intraday) |
| `luno` | ✅ | SA exchange; live crypto vs ZAR |
| `sarb` | ✅ | Authoritative ZAR quotes |
| `frankfurter` | | Clean JSON ECB mirror |
| `erapi` | | open.er-api; fills the ECB Fri→Mon weekend gap |
| `fawazahmed0` | | ~400 currencies, dual-CDN |
| `boc` | | Bank of Canada; independent cross-check |
| `yahoo` | | Unofficial, ToS-prohibited — last resort |

Unknown names in the spec are silently skipped. Full per-source detail, cadence,
and provenance: [SOURCES.md](../SOURCES.md).

### Paid sources (auto-enabled by key)

These need an API key and are added automatically when their env var is present —
you don't have to list them in `-sources`:

| Key | Env var |
|---|---|
| `oxr` | `OPENRATE_OXR_APP_ID` |
| `twelvedata` | `OPENRATE_TWELVEDATA_KEY` |
| `polygon` | `OPENRATE_POLYGON_KEY` |
| `tradermade` | `OPENRATE_TRADERMADE_KEY` |

## Anti-scraping & hardening

When `-ratelimit` is greater than 0, requests to `/api/` paths are limited
per-IP (the embedded UI and its assets are never rate-limited). The server also
serves a restrictive `robots.txt`, sets `X-Content-Type-Options` and
`Referrer-Policy`, and applies `Cache-Control: no-store` to API responses.

## Related

- [API reference](api.md)
- [Embed as a Go library](library.md) — the same options, programmatically
