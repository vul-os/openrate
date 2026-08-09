# openrate for Go

**Go has no FFI here, no shared library, and no platform matrix. You import a
package.**

```go
eng := openrate.NewEngine(openrate.EngineOptions{Base: "ZAR"})
eng.Load(snapshot)                              // rates you already have
c, err := eng.Convert("USD", "ZAR", 100)        // no socket, ever
```

Every other language in `sdks/` reaches this same engine by loading a 6–7 MB C
shared library that puts the Go runtime inside their process, or by supervising
a second process and talking HTTP to it. Go pays for neither. When you read the
Rust or Swift README next to this one, that gap is the thing being described.

## The engine/refresher split is the headline

openrate is two objects, and which one you construct decides whether your
process can talk to the internet:

| | what it does | network |
| --- | --- | --- |
| `openrate.NewEngine` | computes | **none.** Starts no goroutine, opens no socket, reads no environment variable, sends no packet |
| `openrate.NewRefresher` | fetches | the only type in openrate that can open a socket — and constructing it *still* sends nothing; `Refresh` / `Run` do |

A host that never calls `NewRefresher` cannot acquire an outbound dependency by
accident. That is not a convention you have to be careful about; there is no
code path. The split is enforced at the C ABI too — `openrate_new` builds an
engine, `openrate_refresher_new` is a separate call with its own handle, and an
engine handle **refuses** the `"refresh"` method.

Feed an engine without a refresher with `Engine.Load` (in Go) or the `"load"`
method (over the ABI): rates from a cache, a file, a vendor feed, a fixture.

## The two examples

| | what it does | when you want it |
| --- | --- | --- |
| [`examples/direct`](examples/direct) | imports the engine; phase one is zero-packet, phase two is an opt-in `-refresh` | **the default** |
| [`examples/sidecar`](examples/sidecar) | spawns and supervises `openrate serve`, talks HTTP | one refresher shared by many processes; the HTTP shell's rate limiter / CORS / trusted proxies; independent restarts |

```
./sdks/go/examples/run.sh direct     # no network at all
./sdks/go/examples/run.sh refresh    # direct, then a live ECB fetch
./sdks/go/examples/run.sh sidecar
```

Real output, darwin/arm64, Go 1.25.12, openrate 0.1.2:

```
==> direct, engine only (no Refresher is constructed, so no socket opens)
empty engine:  USD->ZAR is ErrUnknownPair (it did not go and look)
loaded:        4 currencies, built_at 2026-08-08T16:00:00Z
USD->ZAR:      100.00 USD = 1842.0000 ZAR (rate 18.420000, 1 hop(s) via [USD ZAR], grade C, 21h22m32s old)
EUR->ZAR:      100.00 EUR = 2001.3330 ZAR (rate 20.013330, 2 hop(s) via [EUR USD ZAR], grade C, 21h22m32s old)
JPY->ZAR:      unknown or unreachable currency pair
rates(ZAR):     EUR=0.049967 GBP=0.042613 USD=0.054289

no Refresher was constructed, so this process opened no socket.

==> direct, with a Refresher (THIS FETCHES FROM ECB)
refresher:     1 source(s), no packet sent yet
  ecb        ok, 29 edges, last_ok 2026-08-09T13:22:33Z
engine now:    30 currencies
EUR->USD:      100.00 EUR = 115.3500 USD (rate 1.153500, 1 hop(s) via [EUR USD], grade C, 61h22m33s old)

==> sidecar (child process over HTTP)
sidecar:   http://127.0.0.1:59849 pid 93248
healthz:   200 OK "ok" (liveness only — no rates implied)
meta:      base=EUR currencies=30 built_at=2026-08-09T13:24:32.167666Z
  ecb        ok, 29 edges
convert:   100.00 EUR = 115.3500 USD (rate 1.153500, 1 hop(s) via [EUR USD], grade C)
rates(EUR): 29 pairs, built_at 2026-08-09T13:24:32.167666Z
rates(XXX): HTTP 200 with 0 pairs — the library would have returned ErrUnknownBase.
```

## Three places the two surfaces genuinely differ

Not stylistic. Each of these has bitten something.

**1. `convert` nests its provenance over HTTP and does not in Go.**
`Engine.Convert` returns a flat `fx.Conversion` — `.Rate`, `.Hops`, `.Path`,
`.Quality` at the top level. `GET /api/v1/convert` returns
`{from, to, amount, result, rate: {rate, hops, path, quality, legs, quotes}}`.
A client that reads `rate` as a number fails to decode. The sidecar example in
this directory got exactly that wrong on its first run.

**2. An unknown base is an error in Go and a 200 with an empty book over HTTP.**
`Engine.Rates("XXX")` returns `ErrUnknownBase`. `GET /api/v1/rates?base=XXX`
answers `200` with `"rates": {}`. A caller checking only the status code reads
"no rates available" as success. The example demonstrates this rather than
describing it.

**3. `/healthz` is liveness, not readiness — and only the library has a real
readiness signal.** This one is a genuine point in favour of the library path,
so it is worth stating rather than glossing.

In-process, `Refresher.Ready(ctx)` blocks until the engine holds at least one
currency, and does not itself fetch. It is an actual signal: openrate closes a
channel the first time a non-empty snapshot lands.

Over HTTP there is nothing equivalent. `/healthz` answers `ok` the instant the
listener binds, before any source has been fetched, so the best a client can do
is **poll `/api/v1/meta` until the currency list is non-empty** — which is what
`examples/sidecar` does, and what every one of this suite's SDKs had to
reimplement independently.

The failure mode if you skip it is nasty, because it is a false green that
disguises itself as user error: the sidecar starts, `/healthz` returns 200,
every conversion answers `{"error":"unknown or unreachable currency pair"}`, and
a program that only checks the status code **exits 0**. "Unknown pair" reads
like a bad currency code, not like "the server has no rates yet". Fail loudly
on an empty result; do not treat a well-formed empty answer as success.

Two further traps in the polling itself, both hit by SDKs in this repo:
`openrate serve`'s anti-scraping limiter is **120 requests per minute per IP**,
so a 200 ms poll gets itself a `429` from the server it is waiting for — back
off. And the meta document is **pretty-printed**, so a readiness check written
against the compact C-ABI shape (`"currencies":[`) never matches the HTTP one
(`"currencies": [`).

One more, worth knowing when writing any client: openrate's `writeJSON` sets a
`200` header and *then* encodes, so a mid-body encoding failure yields a
successful status with a truncated body. Decode errors are real. Do not swallow
them.

## Honesty notes

These apply to every language's SDK. For Go most are moot, and saying which and
why is the point.

1. **The Go runtime in the host process.** Moot. It is already your runtime.
2. **Not fork-safe.** Moot for the direct path — there is no C shared library
   for `fork()` without `exec()` to leave in a broken state.
3. **A 6–7 MB shared library.** Moot. There is none. You link openrate into
   your binary and the linker drops what you do not call.
4. **Platform coverage.** This is the biggest difference, and openrate's C ABI
   coverage is **narrower than llmux's** — do not read one matrix for both
   products. openrate ships: darwin/arm64 built and smoke-tested (6,682,274
   bytes); darwin/amd64 **built but never executed** (7,120,680 bytes);
   linux/amd64 not built locally, with a CI job that has never run;
   **linux/arm64 built nowhere** (llmux does have it); windows/amd64 built
   nowhere. The Go path has no matrix at all: `go get github.com/vul-os/openrate`
   builds from source for whatever `GOOS`/`GOARCH` you are on, including the
   four the shared library does not cover.
5. **Latency is not the reason to embed.** For openrate the reason is even less
   about speed than it is for llmux: the compute is a graph lookup either way.
   The reason is the zero-packet guarantee in point 6.
6. **Is the sidecar the better default for Go?** No. Direct is the default and
   the sidecar is the considered exception. The property you give up by moving
   to HTTP is the whole product: an engine that provably cannot reach the
   network becomes an HTTP client that provably can.

## No streaming

There is no `openrate_stream`, and no streaming API in Go either. openrate
answers from a snapshot it already holds; there is no incremental operation to
stream. llmux, which shares the ABI shape, does define `llmux_stream` because
chat streaming is its main event. The omission is stated rather than left as a
gap a reader has to notice.

## Layout

```
sdks/go/
  examples/
    direct/     engine alone (zero packets), then an opt-in -refresh phase
    sidecar/    spawn `openrate serve`, then HTTP against it
    run.sh      runs all three modes
  README.md
```

There is no `sdks/go/openrate` package, deliberately. openrate's root package
**is** the Go SDK: `github.com/vul-os/openrate` exports `NewEngine`,
`NewRefresher`, `Engine`, `Refresher` and the error values. A wrapper package
would add a name to import and nothing else. `sdks/go` has no `go.mod` either —
it is part of the root module, so `go test ./...` covers it.
