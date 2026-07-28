import React from "react";
import { Eyebrow, Label, Icon, useScrollSpy } from "./ui.jsx";
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
    ["d-interest", "Policy rates"],
  ]},
  { group: "Data & project", items: [
    ["d-sources", "Sources"],
    ["d-contributing", "Contributing"],
  ]},
];

export default function Docs() {
  const ids = NAV.flatMap((g) => g.items.map((i) => i[0]));
  const active = useScrollSpy(ids);
  const go = (e, id) => {
    e.preventDefault();
    document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  return (
    <div className="docs-layout">
      <aside className="docs-side">
        <nav>
          {NAV.map((g) => (
            <div className="docs-group" key={g.group}>
              <Label>{g.group}</Label>
              {g.items.map(([id, label]) => (
                <a key={id} href={`#${id}`} className={active === id ? "on" : ""} onClick={(e) => go(e, id)}>{label}</a>
              ))}
            </div>
          ))}
        </nav>
      </aside>

      <article className="docs-main">
        <header className="docs-head">
          <Eyebrow>Documentation</Eyebrow>
          <h1 className="display d2">Run it, configure it, build on it</h1>
          <p className="prose">
            openrate is one Go binary with the web UI embedded — no database, no runtime
            dependencies, nothing to sign up for. Dual-licensed MIT OR Apache-2.0.
          </p>
        </header>

        <section id="d-quickstart" className="doc-sec">
          <h2>Quick start</h2>
          <p>One command serves the API and the UI on <code>:8080</code>:</p>
          <CodeBlock lang="bash" code={`# clone & run
$ git clone https://github.com/vul-os/openrate
$ cd openrate
$ go run ./cmd/openrate

# → http://localhost:8080  (API + converter UI)`} />
          <p>Every response carries the rate, the calculation behind it and an accuracy grade:</p>
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
              <tr><td><code>-refresh</code></td><td><code>OPENRATE_REFRESH</code></td><td><code>1h</code></td><td>FX source refresh interval (e.g. <code>5m</code>)</td></tr>
              <tr><td><code>-sources</code></td><td><code>OPENRATE_SOURCES</code></td><td><code>ecb,coinbase,luno,sarb</code></td><td>Comma-separated FX source set</td></tr>
              <tr><td><code>-interest-sources</code></td><td><code>OPENRATE_INTEREST_SOURCES</code></td><td><code>bis,sarbrates</code></td><td>Policy-rate sources</td></tr>
              <tr><td><code>-interest-refresh</code></td><td><code>OPENRATE_INTEREST_REFRESH</code></td><td><code>6h</code></td><td>Policy-rate refresh interval</td></tr>
              <tr><td><code>-ratelimit</code></td><td><code>OPENRATE_RATELIMIT</code></td><td><code>120</code></td><td>Per-IP API requests/minute (0 = off)</td></tr>
              <tr><td><code>-cors-origin</code></td><td><code>OPENRATE_CORS_ORIGIN</code></td><td><code>*</code></td><td>Access-Control-Allow-Origin for the JSON API</td></tr>
              <tr><td><code>-trusted-proxies</code></td><td><code>OPENRATE_TRUSTED_PROXIES</code></td><td>—</td><td>Proxy IPs/CIDRs whose <code>X-Forwarded-For</code> is trusted</td></tr>
            </tbody>
          </table>
          <p>Paid sources auto-enable when their key is present — no flag change needed. Copy <code>.env.example</code> to <code>.env</code>:</p>
          <CodeBlock lang="bash" code={`# .env  (optional — every default source is free and keyless)
OPENRATE_OXR_APP_ID=          # Open Exchange Rates
OPENRATE_TWELVEDATA_KEY=      # Twelve Data
OPENRATE_POLYGON_KEY=         # Polygon.io
OPENRATE_TRADERMADE_KEY=      # TraderMade`} />
        </section>

        <section id="d-how" className="doc-sec">
          <h2>How it works</h2>
          <p>
            openrate models currencies as a <b>graph</b>, not a single base. Each source
            publishes quotes in its own native base (ECB in EUR, SARB in ZAR, Coinbase in
            USD); those become edges. Any pair is the product of the rates along the
            <b> shortest path</b> between the two currencies.
          </p>
          <ul className="doc-list">
            <li><b>Direct quotes win.</b> A breadth-first search reaches a pair by the fewest hops first, so a directly-quoted rate always beats a triangulated one.</li>
            <li><b>Freshest breaks ties.</b> Among equal-length paths, the most recent edge is used.</li>
            <li><b>Any base, for free.</b> The base currency is a presentation choice over the same graph — change it with <code>?base=</code>. ZAR is the default, not a privileged position.</li>
            <li><b>No single point of contamination.</b> A bad edge only affects paths that cross it, rather than every pair in the set.</li>
            <li><b>Provenance on every number.</b> The path, the per-leg rates, the sources and the cross-source dispersion all ship with the rate.</li>
          </ul>
        </section>

        <section id="d-accuracy" className="doc-sec">
          <h2>Accuracy model</h2>
          <p>
            Every rate carries a <code>quality</code> block — a grade <b>A–D</b> and a 0–1
            confidence — from five signals: freshness, directness (hops), source authority,
            cross-source agreement, and currency caveats. The factors multiply, and each one
            is returned, so the score is reproducible rather than asserted. Full detail on
            the <a href="#accuracy" onClick={(e) => { e.preventDefault(); location.hash = "#accuracy"; }}>Accuracy</a> section,
            and the constants are pinned in <a href={`${REPO}/blob/main/ACCURACY.md`} target="_blank" rel="noreferrer">ACCURACY.md</a>.
          </p>
        </section>

        <section id="d-endpoints" className="doc-sec">
          <h2>Endpoints</h2>
          <p>All read-only, all JSON, all CORS-enabled. Rate-limited per IP; the embedded UI is not.</p>
          <table className="doc-table">
            <thead><tr><th>Method</th><th>Path</th><th>Description</th></tr></thead>
            <tbody>
              <tr><td>GET</td><td><code>/api/v1/convert</code></td><td><code>?from=USD&amp;to=ZAR&amp;amount=100</code> — convert with full detail</td></tr>
              <tr><td>GET</td><td><code>/api/v1/rates</code></td><td><code>?base=ZAR</code> — all currencies against a base</td></tr>
              <tr><td>GET</td><td><code>/api/v1/meta</code></td><td>Source status, edge counts, currency list</td></tr>
              <tr><td>GET</td><td><code>/api/v1/interest/rates</code></td><td><code>?area=ZA&amp;type=policy</code> — latest policy rates</td></tr>
              <tr><td>GET</td><td><code>/api/v1/interest/series</code></td><td><code>?id=za.policy</code> — one series' history</td></tr>
              <tr><td>GET</td><td><code>/api/v1/interest/meta</code></td><td>Areas, series catalogue, source status</td></tr>
              <tr><td>GET</td><td><code>/healthz</code></td><td>Liveness probe</td></tr>
            </tbody>
          </table>
        </section>

        <section id="d-response" className="doc-sec">
          <h2>Response shape</h2>
          <p>
            <code>/convert</code> and each entry of <code>/rates</code> return the same
            <code>rate</code> object — the number, the per-leg calculation, the contributing
            source quotes, and the quality assessment:
          </p>
          <CodeBlock title="/api/v1/convert?from=USD&to=ZAR&amount=100" method="GET" code={`{
  "result": 1668.77,
  "rate": {
    "rate": 16.687657,
    "hops": 1,
    "age_sec": 28,
    "path": ["USD", "ZAR"],
    "sources": ["coinbase"],
    "legs": [
      { "from": "USD", "to": "ZAR", "rate": 16.687657, "source": "coinbase", "age_sec": 28 }
    ],
    "quotes": [
      { "source": "coinbase", "rate": 16.687657, "age_sec": 28 },
      { "source": "sarb", "rate": 16.777, "age_sec": 67470 }
    ],
    "quality": {
      "grade": "B",
      "confidence": 0.89,
      "freshness": "realtime",
      "directness": "direct",
      "source_class": "exchange",
      "corroboration": { "sources": 2, "spread_bps": 53.54, "stdev_bps": 37.76, "agree": false }
    }
  }
}`} />
        </section>

        <section id="d-interest" className="doc-sec">
          <h2>Policy rates</h2>
          <p>
            A second engine on the same binary carries central-bank policy rates. They are
            flat time series rather than a currency graph, so they get their own store, their
            own slower refresh (policy changes at a meeting, not a tick), and their own
            endpoints — but the same A–D grading contract.
          </p>
          <p>
            The grade earns its keep here: the BIS still publishes legacy pre-euro national
            series whose last observation is from the 1990s. Those grade <b>D</b> and carry an
            explicit date, so nothing in the response lets a 1998 number pass for today's.
          </p>
          <CodeBlock title="/api/v1/interest/rates?area=ZA" method="GET" code={`{
  "count": 1,
  "rates": [
    {
      "series": "za.policy",
      "area": "ZA",
      "type": "policy",
      "name": "South Africa — policy rate",
      "value": 6.75,
      "date": "2026-07-24T00:00:00Z",
      "source": "sarbrates",
      "quality": {
        "grade": "A",
        "confidence": 0.95,
        "freshness": "current",
        "source_class": "central_bank",
        "corroboration": { "sources": 2, "agree": true }
      }
    }
  ]
}`} />
        </section>

        <section id="d-sources" className="doc-sec">
          <h2>Sources</h2>
          <p>
            Rates come from open central-bank files and free public venues — never resold from
            a paid API. Default set: <code>ecb, coinbase, luno, sarb</code>.
          </p>
          <table className="doc-table">
            <thead><tr><th>Source</th><th>Type</th><th>Cadence</th><th>Key?</th></tr></thead>
            <tbody>
              <tr><td>ECB</td><td>central bank (EUR)</td><td>daily</td><td>—</td></tr>
              <tr><td>SARB</td><td>central bank (ZAR, authoritative)</td><td>daily</td><td>—</td></tr>
              <tr><td>Coinbase</td><td>venue (real-time, incl. ZAR)</td><td>~1 min</td><td>—</td></tr>
              <tr><td>Luno</td><td>SA venue (crypto/ZAR)</td><td>real-time</td><td>—</td></tr>
              <tr><td>BIS, SARB</td><td>policy rates</td><td>daily</td><td>—</td></tr>
              <tr><td>open.er-api, fawazahmed0, Bank of Canada, Frankfurter</td><td>open (opt-in)</td><td>daily</td><td>—</td></tr>
              <tr><td>OXR, Twelve Data, Polygon, TraderMade</td><td>paid (auto-enable)</td><td>real-time</td><td>.env</td></tr>
            </tbody>
          </table>
          <p>
            Full catalogue and the "open way" rationale:{" "}
            <a href={`${REPO}/blob/main/SOURCES.md`} target="_blank" rel="noreferrer">SOURCES.md</a>.
          </p>
        </section>

        <section id="d-contributing" className="doc-sec">
          <h2>Contributing</h2>
          <p>
            openrate is dual-licensed MIT OR Apache-2.0 and part of the{" "}
            <a href="https://vulos.org" target="_blank" rel="noreferrer">Vulos</a> ecosystem.
            Issues, PRs and new source adapters are welcome — a source is a small
            <code>sources.Source</code> implementation registered in{" "}
            <code>internal/sources/registry.go</code>.
          </p>
          <p>
            <a className="foot-tag" href={REPO} target="_blank" rel="noreferrer">
              <Icon.GitHub size={15} /> github.com/vul-os/openrate
            </a>
          </p>
        </section>
      </article>
    </div>
  );
}
