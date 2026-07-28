import React from "react";
import { Reveal, Eyebrow, Label } from "./ui.jsx";
import CodeBlock from "./CodeBlock.jsx";

/**
 * Accuracy — how every rate is graded. Mirrors internal/quality/quality.go;
 * the constants here are the constants there, and ACCURACY.md is the contract
 * between the two. If you change a band or a factor, change all three.
 */

const BANDS = [
  { g: "A", from: "≥ 0.90", verdict: "Trust it",       why: "Fresh, directly quoted, and corroborated by independent sources that agree tightly." },
  { g: "B", from: "≥ 0.78", verdict: "Good",           why: "One weak link — usually a single source, or one triangulation hop." },
  { g: "C", from: "≥ 0.60", verdict: "Use with care",  why: "Stale, multi-hop, or a currency whose official rate is not the rate you'd trade at." },
  { g: "D", from: "< 0.60", verdict: "Flagged",        why: "Something is materially wrong with the provenance. Read the caveats before using it." },
];

// The worked example that quality.go's own comment uses: a day-old two-hop
// exchange cross with tight corroboration. Showing the arithmetic is the point
// — a confidence score you cannot reproduce is just a vibe.
const CHAIN = [
  { f: "0.90", l: "freshness",     s: "current — under 26 h" },
  { f: "0.90", l: "directness",    s: "cross — 2 hops" },
  { f: "0.96", l: "source",        s: "exchange" },
  { f: "1.00", l: "corroboration", s: "sources agree ≤ 25 bps" },
];

export default function Accuracy() {
  return (
    <>
      <Reveal className="sec-head">
        <Eyebrow>Methodology</Eyebrow>
        <h2 className="display d2">A rate is only as good as<br /><em>where it came from</em>.</h2>
        <p className="prose">
          Every price openrate serves carries a letter grade and a 0–1 confidence,
          derived from five signals about its provenance. Nothing is hidden behind the
          letter: the factors multiply, and the API returns each one.
        </p>
      </Reveal>

      {/* ── the bands ─────────────────────────────────────────────────── */}
      <Reveal className="bands">
        {BANDS.map((b) => (
          <div className={`band ${b.g}`} key={b.g}>
            <div className="band-h">
              <span className={`seal lg ${b.g}`}>{b.g}</span>
              <span className="band-r num">{b.from}</span>
            </div>
            <Label>{b.verdict}</Label>
            <p>{b.why}</p>
          </div>
        ))}
      </Reveal>

      {/* ── the arithmetic ────────────────────────────────────────────── */}
      <Reveal style={{ marginTop: "clamp(48px, 6vw, 80px)" }}>
        <div className="sec-head">
          <Eyebrow>The arithmetic</Eyebrow>
          <h3 className="display d3">Confidence is a product, not an opinion</h3>
          <p className="prose">
            Each signal contributes a factor between 0 and 1 and they simply multiply.
            Here is a day-old cross-rate off an exchange, whose sources agree closely:
          </p>
        </div>

        <div className="chain">
          {CHAIN.map((c, i) => (
            <React.Fragment key={c.l}>
              {i > 0 && <span className="chain-op">×</span>}
              <div className="chain-term">
                <b className="num">{c.f}</b>
                <Label>{c.l}</Label>
                <span className="chain-note">{c.s}</span>
              </div>
            </React.Fragment>
          ))}
          <span className="chain-op">=</span>
          <div className="chain-term out">
            <b className="num">0.78</b>
            <Label>confidence</Label>
            <span className="chain-note">grade B</span>
          </div>
        </div>
        <p className="note" style={{ marginTop: 16 }}>
          The grade is computed from the <b>published, rounded</b> confidence rather than
          the raw product. Grading the raw value lets the two fields contradict each other
          at a band edge — 0.7776 would publish as <code>0.78</code> next to grade C while
          the documented band for B is ≥ 0.78, and no consumer can reconcile that.
        </p>
      </Reveal>

      {/* ── the five signals ──────────────────────────────────────────── */}
      <Reveal style={{ marginTop: "clamp(48px, 6vw, 80px)" }}>
        <div className="sec-head">
          <Eyebrow>The five signals</Eyebrow>
          <h3 className="display d3">What goes into the score</h3>
        </div>
        <div className="factors">
          <Factor n="01" name="Freshness" tag="age of the oldest edge on the path">
            <code>realtime</code> under 5 min ×1.0 · <code>current</code> under 26 h ×0.9 ·
            <code>daily</code> under 4 days ×0.72 · <code>stale</code> beyond that ×0.45.
            The four-day band exists because fiat markets close: a Friday fix read on
            Monday morning is the latest daily fix, not a stale number, and grading it as
            stale every weekend would be wrong 28% of the time.
          </Factor>
          <Factor n="02" name="Directness" tag="hops through the graph">
            <code>direct</code> 1 hop ×1.0 · <code>cross</code> 2 hops ×0.9 ·
            <code>multi&nbsp;cross</code> 3+ ×0.75. Every hop compounds another venue's
            bid/ask spread into the number.
          </Factor>
          <Factor n="03" name="Source authority" tag="the weakest link on the path">
            <code>official</code> ×1.0 (SARB, ECB, BoC, Frankfurter) ·
            <code>exchange</code> ×0.96 (Coinbase, Luno, Polygon, TraderMade, Twelve Data) ·
            <code>aggregator</code> ×0.92 (open.er-api, fawazahmed0, OXR) ·
            <code>unofficial</code> ×0.7 (Yahoo). A source openrate does not recognise
            grades <code>unknown</code> ×0.8 — unrecognised is not the same as bad, but it
            is not the same as vouched-for either.
          </Factor>
          <Factor n="04" name="Corroboration" tag="do independent sources agree">
            Only sources that quote the pair <strong>directly</strong> are compared, and
            only distinct ones count. Spread ≤25 bps ×1.0 · ≤100 ×0.93 · ≤300 ×0.85 ·
            wider ×0.72. A single source is ×0.88 — which is why a lone quote, however
            fresh, can never reach an A.
          </Factor>
          <Factor n="05" name="Currency caveats" tag="when the official rate isn't the real one">
            <strong>NGN, EGP</strong> — official and parallel-market rates diverge
            materially. <strong>CNY</strong> — managed, and onshore CNY differs from
            offshore CNH. Each caps confidence at ×0.7, so those pairs top out at C.
            Defunct currencies are removed from the graph entirely rather than graded
            down: <code>HRK</code> is not a bad rate, it is not a rate.
          </Factor>
        </div>
      </Reveal>

      {/* ── in the API ────────────────────────────────────────────────── */}
      <Reveal style={{ marginTop: "clamp(48px, 6vw, 80px)" }}>
        <div className="sec-head">
          <Eyebrow>On every response</Eyebrow>
          <h3 className="display d3">Nothing you can't re-derive</h3>
        </div>
        <CodeBlock
          method="GET"
          title="/api/v1/convert?from=USD&to=ZAR&amount=100"
          code={`{
  "result": 1668.77,
  "rate": {
    "rate": 16.687657,
    "hops": 1,
    "path": ["USD", "ZAR"],
    "sources": ["coinbase"],
    "quality": {
      "grade": "B",
      "confidence": 0.89,
      "freshness": "realtime",
      "directness": "direct",
      "source_class": "exchange",
      "corroboration": {
        "sources": 2,
        "spread_bps": 53.54,
        "stdev_bps": 37.76,
        "agree": false
      }
    }
  }
}`}
        />
      </Reveal>
    </>
  );
}

function Factor({ n, name, tag, children }) {
  return (
    <div className="factor">
      <div className="fn">{n}</div>
      <div>
        <div className="fh"><b>{name}</b><span className="tag">{tag}</span></div>
        <div className="fb">{children}</div>
      </div>
    </div>
  );
}
