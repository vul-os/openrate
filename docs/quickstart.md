# Quickstart

Five starting points, one per audience. Pick the one that describes you; none
of them depends on another. If you are not sure which mode you want, read
[Which mode should I choose](deployment-modes.md) first — it is a page long.

Everything below works with no account, no API key and no configuration. The
default source set (ECB, SARB, Coinbase, Luno) is free and needs no keys.

---

## 1. I want to see it work

```bash
git clone https://github.com/vul-os/openrate
cd openrate
go run ./cmd/openrate        # serves the API and the console on :8080
```

Then open <http://localhost:8080> for the console, or:

```bash
curl 'localhost:8080/api/v1/convert?from=USD&to=ZAR&amount=100'
```

The first refresh happens at startup and takes a few seconds. Until it lands,
conversions answer with an unknown-pair error rather than a made-up number —
that is deliberate; see [Proving it sends nothing](zero-network.md).
`curl -sf localhost:8080/readyz` tells you when it has landed, and its `503`
says which source is holding things up.

Only Go is required. There is no build step, no bundler and no third-party Go
module in the whole engine.

## 2. I want to run it as a service

```bash
go install github.com/vul-os/openrate/cmd/openrate@latest
openrate -addr :8080 -base ZAR -refresh 5m
```

or:

```bash
docker build -t openrate . && docker run -p 8080:8080 openrate
```

Then the three decisions worth making on day one:

| Decision | Flag | Why |
|---|---|---|
| How often to fetch | `-refresh 1h` | most sources publish daily or intraday; a minute is wasted egress |
| Which sources | `-sources ecb,coinbase,luno,sarb` | more overlapping sources means better corroboration, which means better grades |
| Whether the console ships | `-tags noui` at build time | saves 66,256 bytes and removes the console entirely |

Full flag and environment reference: [Configuration](configuration.md).
Endpoint reference: [API reference](api.md).

Before you run anything you downloaded, verify it against the release's
`SHA256SUMS` manifest:

```bash
curl -fsSLO https://raw.githubusercontent.com/vul-os/openrate/v0.1.3/scripts/verify.sh
bash verify.sh --tag v0.1.3 --attest openrate_0.1.3_source.zip
```

It fails closed. There is no `--skip-verify`, and an absent manifest is an
error, not "nothing to check". `--attest` also checks the sigstore build
provenance (it needs the `gh` CLI); leave it off and the script says out loud
that provenance was *not* checked, so a pass never implies more than it
checked.

Once it is running, wait for `GET /readyz` to return `200` before you send the
first conversion — `/healthz` answers before any rate has been fetched. That
distinction has its own section in the [API reference](api.md), and it is the
single most common way to get a green start that converts nothing.

## 3. My program is Go

```bash
go get github.com/vul-os/openrate
```

```go
package main

import (
	"context"
	"fmt"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fxsource"
)

func main() {
	// Starts nothing: no goroutine, no socket, no environment read.
	e := openrate.NewEngine(openrate.EngineOptions{Base: "ZAR"})

	// Opt in to fetching. This is the only line here that touches the network.
	r := openrate.NewRefresher(e, openrate.RefreshOptions{
		Sources: fxsource.Build("ecb,coinbase"),
	})

	ctx := context.Background()
	if err := r.Refresh(ctx); err != nil { // or: go r.Run(ctx) on a schedule
		panic(err)
	}

	c, err := e.Convert("USD", "ZAR", 100)
	if err != nil {
		panic(err)
	}
	fmt.Println(c.Result, c.Quality.Grade)
}
```

Three things to know immediately:

- **`NewEngine` is inert.** Construct it behind a feature flag that is off and
  the process is provably unchanged. This is the property most worth
  understanding: [Proving it sends nothing](zero-network.md).
- **`Refresh` is synchronous; `Run` is the loop.** `go r.Run(ctx)` is your
  choice to make, not the library's. Use `r.Ready(ctx)` to block until the
  engine actually holds a rate — not until a health endpoint answered.
- **You do not need a `Refresher` at all.** `e.Load(snapshot)` installs rates
  from a file, a cache, your own treasury feed — anything.

Full reference: [Embed as a Go library](library.md).

## 4. My program is not Go

**Fifteen languages have a package already written**, and every one of them
gives you the same two choices behind one API:

- **Sidecar** — openrate runs as its own process on `127.0.0.1` and you speak
  HTTP to it. Most packages start, supervise and stop that process for you.
  This is the recommended default for every non-Go host, and for most programs
  it is also the correct one.
- **Direct** — the engine runs inside your process, over a C shared library.
  Faster per call, and the only mode where "this process fetches nothing" is
  structural rather than configured. It also brings the Go runtime into your
  address space and is not fork-safe.

[`sdks/README.md`](../sdks/README.md) is the index: which of the two each
language should default to, and why. What follows is how to get one running.

### Run the example for your language

Two prerequisites, both one-off, and you need only the one for the mode you
are trying. From a checkout, at the repository root:

```bash
# Sidecar mode: build the binary the packages will spawn.
go build -o /tmp/openrate ./cmd/openrate && export OPENRATE_BINARY=/tmp/openrate

# Direct mode: build the shared library and point packages at it.
scripts/build-ffi.sh --host-only        # writes dist/ffi/
ext=$([ "$(go env GOOS)" = darwin ] && echo dylib || echo so)
export OPENRATE_LIBRARY="$PWD/dist/ffi/libopenrate-$(go env GOOS)-$(go env GOARCH).$ext"
```

Every package that has a direct mode reads `OPENRATE_LIBRARY`, except C and C++
which link the library at build time — their `Makefile`s take
`OPENRATE_LIB_DIR` instead, defaulting to `dist/ffi/`. Every package that
manages a sidecar reads `OPENRATE_BINARY`.

Then, still from the repository root:

| Language | Direct — in-process | Sidecar — over loopback |
|---|---|---|
| [bun](../sdks/bun/README.md) | `cd sdks/bun && bun run examples/direct.ts` | `cd sdks/bun && bun run examples/sidecar.ts` |
| [C](../sdks/c/README.md) | `cd sdks/c && make && ./direct_convert` | `cd sdks/c && make && ./sidecar_convert` |
| [C++](../sdks/cpp/README.md) | `cd sdks/cpp && make && ./direct_convert` | `cd sdks/cpp && make && ./sidecar_convert` |
| [Deno](../sdks/deno/README.md) | `cd sdks/deno && deno task example:direct` | `cd sdks/deno && deno task example:sidecar` |
| [.NET](../sdks/dotnet/README.md) | `sdks/dotnet/run-examples.sh direct` | `sdks/dotnet/run-examples.sh sidecar` |
| [Elixir](../sdks/elixir/README.md) | — **no direct mode, deliberately** | `cd sdks/elixir && mix run examples/sidecar_convert.exs` |
| [Go](../sdks/go/README.md) | `sdks/go/examples/run.sh direct` | `sdks/go/examples/run.sh sidecar` |
| [Java](../sdks/java/README.md) | `sdks/java/run-examples.sh direct` | `sdks/java/run-examples.sh sidecar` |
| [Kotlin](../sdks/kotlin/README.md) | `sdks/kotlin/run-examples.sh direct` | `sdks/kotlin/run-examples.sh sidecar` |
| [Node](../sdks/node/README.md) | `cd sdks/node && npm install && npm run example:direct` | `cd sdks/node && npm install && npm run example:sidecar` |
| [PHP](../sdks/php/README.md) | `php sdks/php/examples/direct_convert.php` | `php sdks/php/examples/sidecar_convert.php` |
| [Python](../sdks/python/README.md) | `python3 sdks/python/examples/direct_convert.py` | `python3 sdks/python/examples/sidecar_convert.py` |
| [Ruby](../sdks/ruby/README.md) | `ruby sdks/ruby/examples/direct_convert.rb` | `ruby sdks/ruby/examples/sidecar_convert.rb` |
| [Rust](../sdks/rust/README.md) | `sdks/rust/examples/run.sh direct` | `sdks/rust/examples/run.sh sidecar` |
| [Swift](../sdks/swift/README.md) | `sdks/swift/run.sh direct` | `sdks/swift/run.sh sidecar` |

The `run.sh` / `run-examples.sh` scripts build their own prerequisites, so for
Go, Rust, Swift, Java, Kotlin and .NET you can skip the two steps above
entirely. **Every direct example above sends zero packets** — it loads a rate
book you can read in the source and converts against it. Add `--fetch`,
`--refresh` or `refresh` (the flag differs by language; each README says which)
to see the same example build a refresher and go to the network.

### What the two modes look like

Sidecar, in Python — the package starts the process, waits for it to be
**ready**, and hands you a client:

```python
import openrate
from openrate import Client

openrate.start(sources="ecb,coinbase", base_currency="ZAR")  # spawns and waits
with Client() as client:
    answer = client.convert("USD", "ZAR", 100)
    print(answer["result"], answer["rate"]["quality"]["grade"])
```

Direct, in the same language — no process, no socket, no port. Note that this
is the whole change: a different constructor, the same calls, the same
documents back.

```python
from openrate import Engine

with Engine({"base": "ZAR", "quiet": True}) as engine:
    engine.load(edges=[{"from": "USD", "to": "ZAR", "rate": 18.42, "source": "my-desk"}])
    answer = engine.convert("USD", "ZAR", 100)
    print(answer["result"], answer["rate"]["quality"]["grade"])
```

Both return the **same JSON document** the HTTP API publishes — one wire format
for both modes, held by a test that asks one engine the same questions over
both and requires the answers to be equal by value. So the
[API reference](api.md) is the response reference for direct mode too.

### If you would rather write the client yourself

It is a GET and a JSON parse. The one thing not to get wrong is **waiting for
readiness**: `/healthz` answers before any rate has been fetched, so a client
that treats its `200` as "ready" converts nothing and exits successfully.

```python
import json, urllib.request, time

BASE = "http://127.0.0.1:8080"

def wait_ready(timeout=30):
    deadline, last = time.time() + timeout, ""
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(BASE + "/readyz", timeout=2) as r:
                return json.load(r)                      # 200: rates exist
        except urllib.error.HTTPError as e:               # 503: not yet, and why
            last = e.read().decode()
        time.sleep(0.15)
    raise TimeoutError(f"openrate never became ready: {last}")

wait_ready()
with urllib.request.urlopen(BASE + "/api/v1/convert?from=USD&to=ZAR&amount=100") as r:
    doc = json.load(r)
print(doc["result"], doc["rate"]["quality"]["grade"])
```

The `503` body carries each source's `last_error`, which is the difference
between "openrate never became ready" and "ECB is unreachable from this host".
Note also that a proxy configured in `HTTP_PROXY` will, in several languages,
be used for `127.0.0.1` — bypass it for loopback, or set `NO_PROXY=127.0.0.1`.
Both traps are covered in [Troubleshooting](troubleshooting.md).

Two things the response carries that most FX APIs do not, and that are worth
plumbing through rather than discarding: `rate.legs` (the actual path taken and
the arithmetic along it) and `rate.quality` (grade, confidence, freshness,
corroboration and any caveats). See [Accuracy & quality](../ACCURACY.md).

Before committing to direct mode, read the cost list in
[Use it from another language](c-abi.md): it is not fork-safe, no Windows DLL
exists, and of the prebuilt targets only `darwin/arm64` has ever been executed.

## 5. I have my own rates and want the graph, not the feeds

openrate's graph, triangulation and grading work on rates you supply. Nothing
is fetched, and `fxsource` is never imported:

```go
import (
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
)

e := openrate.NewEngine(openrate.EngineOptions{})

g := fx.NewGraph()
g.Replace("treasury", []fx.Edge{
	{From: "USD", To: "ZAR", Rate: 18.50, Source: "treasury", Time: quotedAt},
	{From: "EUR", To: "USD", Rate: 1.09, Source: "treasury", Time: quotedAt},
})
e.Load(g.Materialize(time.Now().UTC()))

c, _ := e.Convert("EUR", "ZAR", 100) // triangulated through USD, graded, with legs
```

`Replace` is per-source: calling it again for `"treasury"` replaces that
source's edges and leaves every other source's alone. That is how a partial
refresh failure keeps the rest of the graph intact.

This is the smallest possible use — `fx` adds 104,672 bytes over an empty
`main` — and the only one with no outbound capability in the binary at all.

The same path exists across the C ABI as `openrate_call(engine, "load", …)`,
so every language package's direct mode can do this too — and there it is the
whole program: an engine that is fed by hand has no refresher, opens no socket,
and is refused if it asks to fetch. There is no HTTP counterpart, because the
server is read-only.

## Where to go next

| If you want… | Read |
|---|---|
| your language's package in detail | [`sdks/README.md`](../sdks/README.md) — the index for all fifteen |
| the C ABI those packages are built on | [Use it from another language](c-abi.md) |
| to understand the numbers | [The graph model](graph-model.md) · [Accuracy & quality](../ACCURACY.md) |
| to know where data comes from | [Sources](../SOURCES.md) |
| every endpoint and response shape | [API reference](api.md) |
| policy and reference interest rates | [Interest rates](interest-rates.md) |
| something to be wrong | [Troubleshooting](troubleshooting.md) |
