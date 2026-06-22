import React from "react";
import { Reveal, Eyebrow } from "./ui.jsx";

// Accuracy documents how openrate grades every rate. Mirrors internal/quality.
export default function Accuracy() {
  return (
    <div className="doc">
      <Reveal as="header" className="hero" style={{ gap: 16 }}>
        <Eyebrow>Methodology</Eyebrow>
        <h1 className="display d1">How every rate is <span className="accent-word">graded</span>.</h1>
        <p className="prose">
          An exchange rate is only as trustworthy as its provenance. openrate attaches a
          quality block to every price — a letter grade and a 0–100% confidence — derived
          from five factors, so you can decide whether a number is good enough for your use.
        </p>
      </Reveal>

      <Reveal className="section">
        <div className="bands">
          {[["A", "≥ 90%", "trust it"], ["B", "≥ 78%", "good"], ["C", "≥ 60%", "use with care"], ["D", "< 60%", "weak / flagged"]].map(([g, r, d]) => (
            <div className={`band b${g}`} key={g}><span className="bg">{g}</span><b>{r}</b><em>{d}</em></div>
          ))}
        </div>
      </Reveal>

      <Reveal className="section">
        <div className="sec-head">
          <Eyebrow>The five factors</Eyebrow>
          <h2 className="display d2">What goes into the score</h2>
        </div>
        <div className="factors">
          <Factor n="01" name="Freshness" desc="age of the underlying quote">
            <code>realtime</code> &lt; 5 min · <code>current</code> &lt; 26 h ·
            <code>daily</code> &lt; 4 days (absorbs the weekend gap) · <code>stale</code> older.
            Fiat markets close on weekends — a Friday rate read on Monday is "daily", not "realtime".
          </Factor>
          <Factor n="02" name="Directness" desc="hops triangulated through">
            <code>direct</code> (1 hop, a directly quoted pair) · <code>cross</code> (2, e.g. via USD) ·
            <code>multi&nbsp;cross</code> (3+). Each hop compounds the bid/ask spread.
          </Factor>
          <Factor n="03" name="Source authority" desc="weakest link on the path sets the class">
            <code>official</code> (central banks — SARB, ECB, Bank of Canada) &gt;
            <code>exchange</code> (Coinbase, Luno) &gt; <code>aggregator</code> (open.er-api, fawazahmed0) &gt;
            <code>unofficial</code> (Yahoo, ToS-flagged).
          </Factor>
          <Factor n="04" name="Corroboration" desc="cross-source agreement for the exact pair">
            We compare every independent source that <em>directly</em> quotes the pair and report
            the spread in basis points. Many sources agreeing tightly → high confidence; a single
            uncorroborated source or a wide spread → lower.
          </Factor>
          <Factor n="05" name="Currency caveats" desc="a rate-quality problem no plumbing fixes">
            <strong>NGN, EGP</strong> — official and parallel-market rates can differ materially.
            <strong> CNY</strong> — managed; onshore (CNY) vs offshore (CNH) differ. Defunct
            currencies (e.g. HRK) are removed entirely.
          </Factor>
        </div>
      </Reveal>

      <Reveal className="section">
        <div className="sec-head">
          <Eyebrow>Coverage</Eyebrow>
          <h2 className="display d2">Where it's strong vs thin</h2>
        </div>
        <div className="card">
          <table className="rates">
            <thead><tr><th>Tier</th><th>Currencies</th><th>Why</th></tr></thead>
            <tbody>
              <tr><td><span className="grade sm bA">A</span></td><td className="ccy">USD EUR GBP JPY CHF AUD CAD ZAR</td><td className="muted">multiple sources, direct &amp; fresh</td></tr>
              <tr><td><span className="grade sm bC">C</span></td><td className="ccy">NGN KES GHS EGP MAD BWP AED SAR</td><td className="muted">fewer sources, triangulated</td></tr>
              <tr><td><span className="grade sm bD">!</span></td><td className="ccy">NGN EGP CNY</td><td className="muted">official rate may differ from transactable</td></tr>
            </tbody>
          </table>
        </div>
      </Reveal>

      <Reveal className="section">
        <div className="sec-head">
          <Eyebrow>In the API</Eyebrow>
          <h2 className="display d2">Read it on every response</h2>
        </div>
        <pre className="code-block">{`GET /api/v1/convert?from=USD&to=ZAR

"rate": {
  "rate": 16.44, "hops": 1, "age_sec": 4,
  "sources": ["coinbase"],
  "quality": {
    "grade": "B", "confidence": 0.89,
    "freshness": "realtime", "directness": "direct",
    "source_class": "exchange",
    "corroboration": { "sources": 4, "spread_bps": 29, "agree": true },
    "caveats": []
  }
}`}</pre>
      </Reveal>
    </div>
  );
}

function Factor({ n, name, desc, children }) {
  return (
    <div className="factor">
      <div className="fn">{n}</div>
      <div>
        <div className="fhead"><b>{name}</b><span className="muted">{desc}</span></div>
        <div className="fbody">{children}</div>
      </div>
    </div>
  );
}
