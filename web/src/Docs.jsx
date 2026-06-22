import React from "react";
import { Eyebrow, useScrollSpy, GitHubIcon } from "./ui.jsx";
import CodeBlock from "./CodeBlock.jsx";

const REPO = "https://github.com/vul-os/openrate";

const NAV = [
  { group: "Getting started", items: [
    ["d-quickstart", "Quick start"],
    ["d-install", "Install & run"],
    ["d-config", "Configuration"],
  ]},
  { group: "Concepts", items: [
    ["d-how", "How it works"],
    ["d-accuracy", "Accuracy model"],
  ]},
  { group: "API", items: [
    ["d-endpoints", "Endpoints"],
    ["d-response", "Response shape"],
  ]},
  { group: "Data & project", items: [
    ["d-sources", "Sources"],
    ["d-cloud", "Cloud vs self-host"],
    ["d-contributing", "Contributing"],
  ]},
];

export default function Docs() {
  const ids = NAV.flatMap((g) => g.items.map((i) => i[0]));
  const active = useScrollSpy(ids);
  const go = (e, id) => { e.preventDefault(); document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" }); };

  return (
    <div className="docs-layout">
      <aside className="docs-side">
        <nav>
          {NAV.map((g) => (
            <div className="docs-group" key={g.group}>
              <div className="docs-group-h">{g.group}</div>
              {g.items.map(([id, label]) => (
                <a key={id} href={`#${id}`} className={active === id ? "on" : ""} onClick={(e) => go(e, id)}>{label}</a>
              ))}
            </div>
          ))}
          <a className="docs-repo" href={REPO} target="_blank" rel="noreferrer"><GitHubIcon size={15} /> View on GitHub</a>
        </nav>
      </aside>

      <article className="docs-main">
        <header className="docs-head">
          <Eyebrow>Documentation</Eyebrow>
          <h1 className="display d2">Run, configure, and integrate open&nbsp;rate</h1>
          <p className="prose">openrate is a single Go binary with the web UI embedded — no database, no dependencies. Self-host it free (MIT), or use the managed API. This covers both.</p>
        </header>

        <section id="d-quickstart" className="doc-sec">
          <h2>Quick start</h2>
          <p>Run it from source in one command — it serves the API and the UI on <code>:8080</code>:</p>
          <CodeBlock lang="bash" code={`# clone & run
$ git clone https://github.com/vul-os/openrate
$ cd openrate
$ go run ./cmd/openrate

# → http://localhost:8080  (API + converter UI)`} />
          <p>Convert with a single request — every response includes the rate, the calculation and an accuracy grade:</p>
          <CodeBlock lang="bash" code={`$ curl "localhost:8080/api/v1/convert?from=USD&to=ZAR&amount=100"`} />
        </section>

        <section id="d-install" className="doc-sec">
          <h2>Install &amp; run</h2>
          <p>Three ways to run it. All produce the same self-contained server.</p>
          <h3>Binary</h3>
          <CodeBlock lang="bash" code={`$ go build -o openrate ./cmd/openrate
$ ./openrate -addr :8080 -base ZAR -refresh 5m`} />
          <h3>Docker</h3>
          <CodeBlock lang="bash" code={`$ docker build -t openrate .
$ docker run -p 8080:8080 openrate
# pass flags/env:
$ docker run -p 8080:8080 -e OPENRATE_BASE=USD openrate`} />
          <h3>go install</h3>
          <CodeBlock lang="bash" code={`$ go install github.com/vul-os/openrate/cmd/openrate@latest
$ openrate -base ZAR`} />
        </section>

        <section id="d-config" className="doc-sec">
          <h2>Configuration</h2>
          <p>Every flag has a matching environment variable (flags win). A <code>.env</code> file is auto-loaded if present.</p>
          <table className="doc-table">
            <thead><tr><th>Flag</th><th>Env</th><th>Default</th><th>Description</th></tr></thead>
            <tbody>
              <tr><td><code>-addr</code></td><td><code>OPENRATE_ADDR</code></td><td><code>:8080</code></td><td>Listen address</td></tr>
              <tr><td><code>-base</code></td><td><code>OPENRATE_BASE</code></td><td><code>ZAR</code></td><td>Default presentation base currency</td></tr>
              <tr><td><code>-refresh</code></td><td><code>OPENRATE_REFRESH</code></td><td><code>1h</code></td><td>Source refresh interval (e.g. <code>5m</code>)</td></tr>
              <tr><td><code>-sources</code></td><td><code>OPENRATE_SOURCES</code></td><td><code>ecb,coinbase,luno,sarb</code></td><td>Comma-separated source set</td></tr>
              <tr><td><code>-ratelimit</code></td><td><code>OPENRATE_RATELIMIT</code></td><td><code>120</code></td><td>Per-IP API requests/minute (0 = off)</td></tr>
            </tbody>
          </table>
          <p>Paid sources auto-enable when their key is present in <code>.env</code> — no flag change needed. Copy <code>.env.example</code> to <code>.env</code>:</p>
          <CodeBlock lang="bash" code={`# .env  (optional — free sources need no keys)
OPENRATE_OXR_APP_ID=          # Open Exchange Rates
OPENRATE_TWELVEDATA_KEY=      # Twelve Data
OPENRATE_POLYGON_KEY=         # Polygon.io
OPENRATE_TRADERMADE_KEY=      # TraderMade`} />
        </section>

        <section id="d-how" className="doc-sec">
          <h2>How it works</h2>
          <p>openrate models currencies as a <b>graph</b>, not a single base. Each source publishes quotes in its own native base (ECB in EUR, SARB in ZAR, Coinbase in USD); those become edges. Any pair is the product of rates along the <b>shortest path</b> between the two currencies.</p>
          <ul className="doc-list">
            <li><b>Direct quotes win.</b> A breadth-first search reaches a pair by the fewest hops first, so a directly-quoted rate always beats a triangulated one.</li>
            <li><b>Freshest breaks ties.</b> Among equal-length paths, the most recent edge is used.</li>
            <li><b>Any base, for free.</b> The base currency is just a presentation choice over the same graph — change it with <code>?base=</code>.</li>
            <li><b>Provenance on every number.</b> The path, the per-leg rates, the sources, and cross-source dispersion all ship with the rate.</li>
          </ul>
        </section>

        <section id="d-accuracy" className="doc-sec">
          <h2>Accuracy model</h2>
          <p>Every rate carries a <code>quality</code> block — a grade <b>A–D</b> and a 0–1 confidence — from five signals: freshness, directness (hops), source authority, cross-source agreement, and currency caveats. Full detail on the <a href="#accuracy" onClick={(e) => { e.preventDefault(); location.hash = "#accuracy"; }}>Accuracy</a> page.</p>
        </section>

        <section id="d-endpoints" className="doc-sec">
          <h2>Endpoints</h2>
          <table className="doc-table">
            <thead><tr><th>Method</th><th>Path</th><th>Description</th></tr></thead>
            <tbody>
              <tr><td>GET</td><td><code>/api/v1/convert</code></td><td><code>?from=USD&amp;to=ZAR&amp;amount=100</code> — convert with full detail</td></tr>
              <tr><td>GET</td><td><code>/api/v1/rates</code></td><td><code>?base=ZAR</code> — all currencies vs base</td></tr>
              <tr><td>GET</td><td><code>/api/v1/meta</code></td><td>Sources, freshness, currency list</td></tr>
              <tr><td>GET</td><td><code>/healthz</code></td><td>Liveness probe</td></tr>
            </tbody>
          </table>
        </section>

        <section id="d-response" className="doc-sec">
          <h2>Response shape</h2>
          <p>Both <code>/convert</code> and each entry of <code>/rates</code> return the same <code>rate</code> object — the number, the per-leg calculation, the contributing source quotes, and the quality assessment:</p>
          <CodeBlock title="/api/v1/convert?from=USD&to=ZAR&amount=100" method="GET" code={`{
  "result": 1640.48,
  "rate": {
    "rate": 16.4048,
    "hops": 1,
    "age_sec": 4,
    "path": ["USD", "ZAR"],
    "sources": ["coinbase"],
    "legs": [
      { "from": "USD", "to": "ZAR", "rate": 16.4048, "source": "coinbase", "age_sec": 4 }
    ],
    "quotes": [
      { "source": "coinbase", "rate": 16.3947, "age_sec": 4 },
      { "source": "sarb", "rate": 16.4775, "age_sec": 259200 }
    ],
    "quality": {
      "grade": "B",
      "confidence": 0.89,
      "freshness": "realtime",
      "directness": "direct",
      "source_class": "exchange",
      "corroboration": { "sources": 4, "spread_bps": 43.6, "stdev_bps": 19.4, "agree": true },
      "caveats": []
    }
  }
}`} />
        </section>

        <section id="d-sources" className="doc-sec">
          <h2>Sources</h2>
          <p>Rates come from open central-bank files and free public venues — never resold from a paid API. Default set: <code>ecb, coinbase, luno, sarb</code>.</p>
          <table className="doc-table">
            <thead><tr><th>Source</th><th>Type</th><th>Cadence</th><th>Key?</th></tr></thead>
            <tbody>
              <tr><td>ECB</td><td>central bank (EUR)</td><td>daily</td><td>—</td></tr>
              <tr><td>SARB</td><td>central bank (ZAR, authoritative)</td><td>daily</td><td>—</td></tr>
              <tr><td>Coinbase</td><td>venue (real-time, incl. ZAR)</td><td>~1 min</td><td>—</td></tr>
              <tr><td>Luno</td><td>SA venue (crypto/ZAR)</td><td>real-time</td><td>—</td></tr>
              <tr><td>open.er-api, fawazahmed0, Bank of Canada, Frankfurter</td><td>open (opt-in)</td><td>daily</td><td>—</td></tr>
              <tr><td>OXR, Twelve Data, Polygon, TraderMade</td><td>paid (auto-enable)</td><td>real-time</td><td>.env</td></tr>
            </tbody>
          </table>
          <p>Full catalogue and the "open way" rationale: <a href={`${REPO}/blob/main/SOURCES.md`} target="_blank" rel="noreferrer">SOURCES.md</a>.</p>
        </section>

        <section id="d-cloud" className="doc-sec">
          <h2>Cloud vs self-host</h2>
          <p><b>Self-host</b> is free forever — the binary above, your own source keys, no limits you don't set. <b>openrate Cloud</b> is the managed endpoint: API keys, real-time paid feeds, history and an SLA, billed by requests. See <a href="#pricing" onClick={(e) => { e.preventDefault(); location.hash = "#pricing"; }}>Pricing</a>.</p>
        </section>

        <section id="d-contributing" className="doc-sec">
          <h2>Contributing</h2>
          <p>openrate is MIT-licensed and part of the <a href="https://vulos.org" target="_blank" rel="noreferrer">Vulos</a> ecosystem. Issues, PRs and new source adapters are welcome — a source is just a small <code>sources.Source</code> implementation registered in <code>internal/sources/registry.go</code>.</p>
          <p><a className="docs-repo inline" href={REPO} target="_blank" rel="noreferrer"><GitHubIcon size={15} /> github.com/vul-os/openrate</a></p>
        </section>
      </article>
    </div>
  );
}
