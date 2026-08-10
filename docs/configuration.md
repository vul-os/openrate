# Configuration

openrate is configured with command-line flags, each backed by an environment
variable. Real environment variables and flags both win over a `.env` file.

## Flags & environment variables

| Flag | Env var | Default | Description |
|---|---|---|---|
| `-addr` | `OPENRATE_ADDR` | `:8080` | Listen address |
| `-base` | `OPENRATE_BASE` | `ZAR` | Default presentation base currency |
| `-refresh` | `OPENRATE_REFRESH` | `1h` | Source refresh interval (Go duration, e.g. `30m`) |
| `-sources` | `OPENRATE_SOURCES` | `ecb,coinbase,luno,sarb` | Comma-separated FX source spec |
| `-interest-sources` | `OPENRATE_INTEREST_SOURCES` | `bis,sarbrates` | Comma-separated interest-rate source spec (empty falls back to the default; a spec that matches no source, e.g. `none`, disables the engine) |
| `-interest-refresh` | `OPENRATE_INTEREST_REFRESH` | `6h` | Interest-rate refresh interval (Go duration) |
| `-ratelimit` | `OPENRATE_RATELIMIT` | `120` | API requests/minute per client network prefix — `/64` for IPv6, `/32` for IPv4 (anti-scraping; `0` disables) |
| `-cors-origin` | `OPENRATE_CORS_ORIGIN` | `*` | `Access-Control-Allow-Origin` for the JSON API |
| `-trusted-proxies` | `OPENRATE_TRUSTED_PROXIES` | _(none)_ | Comma-separated proxy IPs/CIDRs whose `X-Forwarded-For` is trusted for rate-limiting |
| _(env only)_ | `OPENRATE_ALLOW_PRIVATE_SOURCES` | _(unset)_ | `1` lets a source **hostname** resolve to a private, loopback or link-local address. Off by default — see [Outbound dial policy](#outbound-dial-policy) |

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

## CORS

The JSON API is public and read-only, so it answers cross-origin requests with
`Access-Control-Allow-Origin: *` by default. To restrict which web origin may
call it from a browser, set `-cors-origin` / `OPENRATE_CORS_ORIGIN` to a single
origin, e.g. `https://app.example.com`. (Non-browser clients are unaffected;
CORS is a browser policy.)

## Trusted proxies & client identity

### What counts as "one client"

Since 0.1.5 the anti-scraping rate limiter buckets by **network prefix**, not by
individual address:

| Family | Bucket | Why |
|---|---|---|
| IPv4 | `/32` — the whole address | The smallest thing an operator hands out, and routinely shared by a CGNAT'd or office-NAT'd population. Aggregating any coarser would limit unrelated people together. |
| IPv6 | `/64` | SLAAC gives every LAN a `/64` and every host on it as many addresses as it cares to generate. Bucketing per `/128` meant the limit never bound on a v6 client at all: 1000 requests from one `/64` at `rpm=1` were all allowed. |

An IPv4-mapped IPv6 address (`::ffff:1.2.3.4`) is unmapped first, so it shares
the bucket of the plain `1.2.3.4` rather than getting a second one. Masking also
drops any zone, so a varying `fe80::1%zone` cannot mint buckets.

**This means clients sharing a `/64` now share a budget.** If several machines
sit behind one IPv6 prefix — a home LAN, an office, a provider that assigns each
VM a fragment of a shared `/64` — their requests count against the same 120/min.
`/64` rather than `/56` or `/48` is the deliberate stopping point: it is the
smallest unit a single party can be assumed to control entirely and the largest
that is unlikely to span unrelated customers. A client that genuinely holds a
`/56` still gets 256 buckets, which is a bounded 256× budget rather than the
unbounded one `/128` gave it.

### The bucket map is capped

The live bucket map holds at most **65536** entries (~10 MB), because an
attacker cycling source prefixes mints keys faster than the 15-minute idle sweep
reclaims them — a memory-exhaustion vector keyed by attacker-chosen input.

At the cap, a victim is chosen from a small random sample, but **only among
buckets that have refilled to full** — whose state is byte-for-byte what a fresh
bucket would hold. Evicting a full bucket therefore gives its owner nothing. A
plain LRU would not have that property: an attacker over budget on one key could
spray fresh keys until their own drained bucket fell off the end, then return to
it with a full allowance. When nothing in the sample is full, the request is
charged to a single shared overflow bucket instead of growing the map — under a
sustained key flood that makes newly-seen clients share one budget, which is the
deliberate trade for a memory ceiling that cannot be turned into free allowance.

### Trusted proxies

By default the client address is the connection's `RemoteAddr` and the
`X-Forwarded-For` (XFF) header is **not** trusted — otherwise a directly exposed
client could rotate XFF to mint a fresh bucket per request and bypass the limit.

If openrate runs behind a reverse proxy / CDN that sets XFF, list that proxy's
addresses in `-trusted-proxies` / `OPENRATE_TRUSTED_PROXIES` (comma-separated IPs
or CIDRs, e.g. `10.0.0.0/8,203.0.113.4`). XFF is then honoured, but only for
requests whose direct peer is one of those trusted proxies.

The hop taken is the **right-most** XFF entry that is not itself a configured
trusted proxy — not the left-most. Standard reverse proxies (nginx's
`$proxy_add_x_forwarded_for`, Cloudflare) *append* the address they observed
rather than replacing the header, so the genuine client sits to the right and
the forgeable, client-supplied hops sit to the left.

## Outbound dial policy

Two rules govern where a rate-feed client may connect, and neither is
configurable per source.

**No redirects, ever.** All sixteen outbound clients refuse to follow one. Every
endpoint openrate ships answers `200` directly, so no source needs it, and a
feed that tries produces `<source>: status 302` — a refusal that deliberately
does not echo the redirect target.

**A source *hostname* must resolve to a public address.** A private, loopback or
link-local result is refused before connect. This is the DNS-rebinding half: a
name that answers a public address on the lookup an operator does by hand and
`169.254.169.254` on the one the process does would otherwise reach the cloud
metadata service on the first fetch. **A host allow-list cannot close that**, because
the name in the allow-list is the name that rebinds.

Two deliberate exceptions keep the check complete without being noisy:

- **An IP literal in a configured URL is dialled as written.** `http://10.4.2.9/rates`
  is your own decision, and an exact address cannot rebind — there is no second
  lookup to differ from the first.
- **A host named in `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` skips the check**,
  because the transport dials the proxy and the proxy resolves the feed. An
  ordinary corporate proxy on a private address is not the attack.

The refusal reads *"refusing to connect: this source's hostname resolved to a
non-public address (loopback, link-local or private)"* and **names no address**,
on purpose: it reaches `Status.LastError`, which `/readyz` and `/api/v1/meta`
publish without authentication, so echoing the resolved IP would leak either
your internal addressing or a confirmation to whoever chose it.

If a source of yours genuinely is on a private network **and is reached by
name**, set `OPENRATE_ALLOW_PRIVATE_SOURCES=1`. That is the only way past it, it
has to be set deliberately, and the default is safe.

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

## Interest-rate engine

A separate, flat time-series engine (central-bank policy and reference rates)
served under `/api/v1/interest/*`. It runs by default from `bis,sarbrates` (49
central banks' policy rates + the South African ZARONIA family, no keys needed)
on its own `-interest-refresh` cadence. Set `OPENRATE_FRED_API_KEY` to auto-add
the US FRED benchmark series. Full detail: [interest-rates.md](interest-rates.md).

## Anti-scraping & hardening

When `-ratelimit` is greater than 0, requests to `/api/` paths are limited per
client network prefix — a `/64` for IPv6, a `/32` for IPv4 (the embedded UI and
its assets are never rate-limited); see [above](#what-counts-as-one-client). The server also
serves a restrictive `robots.txt`, sets `X-Content-Type-Options` and
`Referrer-Policy`, and applies `Cache-Control: no-store` to API responses.

## Related

- [API reference](api.md)
- [Embed as a Go library](library.md) — the same options, programmatically
