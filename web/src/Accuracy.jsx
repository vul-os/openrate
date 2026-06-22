import React from "react";
import { Reveal, Eyebrow } from "./ui.jsx";
import CodeBlock from "./CodeBlock.jsx";

// Accuracy — how openrate grades every rate. Mirrors internal/quality.
export default function Accuracy() {
  return (
    <div className="doc">
      <Reveal as="header" className="hero" style={{ gap: 16 }}>
        <Eyebrow>Methodology</Eyebrow>
        <h1 className="display d1">How every rate is <span className="accent-word">graded</span>.</h1>
        <p className="prose">
          An exchange rate is only as trustworthy as its provenance. openrate attaches a
          quality block to every price — a letter grade and a 0–100% confidence — derived
          from five signals, so you can decide whether a number is good enough for your use.
        </p>
      </Reveal>

      {/* confidence meter */}
      <Reveal className="section">
        <div className="meter">
          <div className="meter-track">
            <div className="meter-seg sD"><span>D</span><em>&lt;60%</em></div>
            <div className="meter-seg sC"><span>C</span><em>≥60%</em></div>
            <div className="meter-seg sB"><span>B</span><em>≥78%</em></div>
            <div className="meter-seg sA"><span>A</span><em>≥90%</em></div>
          </div>
          <div className="meter-labels">
            <span>weak · flagged</span>
            <span>use with care</span>
            <span>good</span>
            <span>trust it</span>
          </div>
        </div>
      </Reveal>

      <Reveal className="section">
        <div className="sec-head">
          <Eyebrow>The five signals</Eyebrow>
          <h2 className="display d2">What goes into the score</h2>
        </div>
        <div className="factors">
          <Factor n="01" name="Freshness" tag="age of the quote">
            <code>realtime</code> &lt; 5 min · <code>current</code> &lt; 26 h ·
            <code>daily</code> &lt; 4 days (absorbs the weekend gap) · <code>stale</code> older.
            Fiat markets close on weekends — a Friday rate read on Monday is "daily", not "realtime".
          </Factor>
          <Factor n="02" name="Directness" tag="hops triangulated">
            <code>direct</code> (1 hop, a quoted pair) · <code>cross</code> (2, e.g. via USD) ·
            <code>multi&nbsp;cross</code> (3+). Each hop compounds the bid/ask spread.
          </Factor>
          <Factor n="03" name="Source authority" tag="weakest link on the path">
            <code>official</code> (central banks — SARB, ECB, BoC) &gt; <code>exchange</code> (Coinbase,
            Luno, Polygon) &gt; <code>aggregator</code> (open.er-api, OXR) &gt; <code>unofficial</code> (Yahoo).
          </Factor>
          <Factor n="04" name="Corroboration" tag="cross-source agreement">
            Every independent source that <em>directly</em> quotes the pair is compared; we report the
            standard deviation and spread in basis points. Many sources agreeing tightly → high
            confidence; a single source or a wide spread → lower.
          </Factor>
          <Factor n="05" name="Currency caveats" tag="rate-quality flags">
            <strong>NGN, EGP</strong> — official vs parallel-market rates differ materially.
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
        <div className="cov-grid">
          <CovCard grade="A" cls="bA" ccy="USD EUR GBP JPY CHF AUD CAD ZAR" why="multiple independent sources, direct & fresh" />
          <CovCard grade="C" cls="bC" ccy="NGN KES GHS EGP MAD BWP AED SAR" why="fewer sources, triangulated through USD" />
          <CovCard grade="!" cls="bD" ccy="NGN EGP CNY" why="official rate may differ from the transactable one" />
        </div>
      </Reveal>

      <Reveal className="section">
        <div className="sec-head">
          <Eyebrow>In the API</Eyebrow>
          <h2 className="display d2">Read it on every response</h2>
        </div>
        <CodeBlock
          title="/api/v1/convert?from=USD&to=ZAR"
          code={`{
  "result": 1639.47,
  "rate": {
    "rate": 16.3947,
    "hops": 1,
    "sources": ["coinbase"],
    "quality": {
      "grade": "B",
      "confidence": 0.89,
      "freshness": "realtime",
      "directness": "direct",
      "source_class": "exchange",
      "corroboration": {
        "sources": 4,
        "spread_bps": 43.62,
        "stdev_bps": 19.39,
        "agree": true
      }
    }
  }
}`}
        />
      </Reveal>
    </div>
  );
}

function Factor({ n, name, tag, children }) {
  return (
    <div className="factor">
      <div className="fn">{n}</div>
      <div>
        <div className="fhead"><b>{name}</b><span className="ftag">{tag}</span></div>
        <div className="fbody">{children}</div>
      </div>
    </div>
  );
}

function CovCard({ grade, cls, ccy, why }) {
  return (
    <div className="cov-card">
      <span className={`grade lg ${cls}`}>{grade}</span>
      <div className="cov-ccy">{ccy.split(" ").map((c) => <span className="tag-ccy" key={c}>{c}</span>)}</div>
      <p>{why}</p>
    </div>
  );
}
