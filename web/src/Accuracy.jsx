import React from "react";

// Accuracy documents how openrate grades every rate. It mirrors the logic in
// internal/quality so the UI explanation stays the source of truth for users.
export default function Accuracy() {
  return (
    <div className="doc">
      <section className="card">
        <h2>How openrate grades accuracy</h2>
        <p className="muted">
          An exchange rate is only as good as where it came from. openrate attaches a
          <strong> quality</strong> block to every price — a letter grade and a 0–100%
          confidence — so you can decide whether a number is good enough for your use.
          The grade is derived from five factors, multiplied into one confidence score.
        </p>
        <div className="bands">
          {[["A", "≥ 90%", "trust it"], ["B", "≥ 78%", "good"], ["C", "≥ 60%", "use with care"], ["D", "< 60%", "weak / flagged"]].map(([g, r, d]) => (
            <div className={`band b${g}`} key={g}><span className="bg">{g}</span><b>{r}</b><em>{d}</em></div>
          ))}
        </div>
      </section>

      <section className="card">
        <h3>The five factors</h3>
        <Factor name="Freshness" desc="How old the underlying quote is.">
          <code>realtime</code> &lt; 5 min · <code>current</code> &lt; 26 h ·
          <code>daily</code> &lt; 4 days (covers a weekend gap) · <code>stale</code> older.
          Fiat markets close on weekends, so a Friday rate read on Monday is "daily", not "realtime".
        </Factor>
        <Factor name="Directness" desc="How many hops the cross-rate was triangulated through.">
          <code>direct</code> (1 hop, a directly quoted pair) ·
          <code>cross</code> (2 hops, e.g. via USD) ·
          <code>multi&nbsp;cross</code> (3+). Each hop compounds the bid/ask spread.
        </Factor>
        <Factor name="Source authority" desc="The weakest link on the path sets the class.">
          <code>official</code> (central banks: SARB, ECB, Bank of Canada) &gt;
          <code>exchange</code> (Coinbase, Luno) &gt;
          <code>aggregator</code> (open.er-api, fawazahmed0) &gt;
          <code>unofficial</code> (Yahoo, ToS-flagged).
        </Factor>
        <Factor name="Corroboration" desc="Cross-source agreement for the exact pair.">
          We compare every independent source that <em>directly</em> quotes the pair and
          report the spread in basis points. Many sources agreeing tightly → high
          confidence; a single uncorroborated source or a wide spread → lower.
        </Factor>
        <Factor name="Currency caveats" desc="Some currencies have a rate-quality problem no plumbing fixes.">
          <strong>NGN, EGP</strong> — official and parallel-market rates can differ materially.
          <strong> CNY</strong> — managed; onshore (CNY) vs offshore (CNH) differ.
          Defunct currencies (e.g. HRK) are removed entirely.
        </Factor>
      </section>

      <section className="card">
        <h3>Where coverage is strong vs thin</h3>
        <table className="rates">
          <thead><tr><th>Tier</th><th>Currencies</th><th>Why</th></tr></thead>
          <tbody>
            <tr><td><span className="grade sm bA">A</span></td><td>USD, EUR, GBP, JPY, CHF, AUD, CAD, ZAR (vs majors)</td><td>multiple independent sources, direct &amp; fresh</td></tr>
            <tr><td><span className="grade sm bC">C</span></td><td>NGN, KES, GHS, EGP, MAD, BWP, AED, SAR</td><td>fewer sources, triangulated; some managed/parallel</td></tr>
            <tr><td><span className="grade sm bD">flagged</span></td><td>NGN, EGP, CNY</td><td>official rate may differ from the transactable rate</td></tr>
          </tbody>
        </table>
        <p className="muted">Every response carries its own grade — this table is just the typical shape.</p>
      </section>

      <section className="card">
        <h3>Read it in the API</h3>
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
      </section>
    </div>
  );
}

function Factor({ name, desc, children }) {
  return (
    <div className="factor">
      <div className="fhead"><b>{name}</b> <span className="muted">{desc}</span></div>
      <div className="fbody">{children}</div>
    </div>
  );
}
