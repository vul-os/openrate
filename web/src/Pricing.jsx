import React from "react";
import { Reveal, Eyebrow } from "./ui.jsx";
import CodeBlock from "./CodeBlock.jsx";

const TIERS = [
  { name: "Free", zar: "R0", usd: "$0", reqs: "10,000 / mo", rl: "60/min",
    feat: ["Open daily sources (ECB, SARB, Coinbase, Luno)", "Accuracy grade on every rate", "Community support"], cta: "Start free" },
  { name: "Developer", zar: "R165", usd: "~$9", reqs: "500,000 / mo", rl: "120/min", popular: true,
    feat: ["Everything in Free", "Real-time + dispersion stats", "All cross-rate calculations", "Email support · 99.9% SLA"], cta: "Get API key" },
  { name: "Startup", zar: "R720", usd: "~$39", reqs: "5,000,000 / mo", rl: "600/min",
    feat: ["Everything in Developer", "History & time-series", "All paid feeds", "Overage R9 / 100k"], cta: "Get API key" },
  { name: "Business", zar: "R2,750", usd: "~$149", reqs: "50,000,000 / mo", rl: "2,000/min",
    feat: ["Everything in Startup", "Custom base currencies", "Priority support · 99.95% SLA", "Overage R5 / 100k"], cta: "Get API key" },
  { name: "Enterprise", zar: "Custom", usd: "", reqs: "Committed", rl: "20,000/min",
    feat: ["On-prem / self-host support", "Dedicated infra · DPA", "99.99% SLA", "Solution engineering"], cta: "Talk to us" },
];

const COMPARE = {
  cols: ["openrate", "Open Exch. Rates", "currencylayer", "ExchangeRate-API", "Twelve Data"],
  rows: [
    ["Entry paid / mo", ["$9", "y"], ["$12", ""], ["$10", ""], ["$10", ""], ["$29", ""]],
    ["Free tier", ["10k / mo", "y"], ["1k / mo", ""], ["100 / mo", ""], ["1.5k / mo", ""], ["800 / day", ""]],
    ["Real-time", ["✓", "y"], ["✓ 60s", ""], ["✓", ""], ["hourly", ""], ["✓", ""]],
    ["Accuracy grade + stats", ["✓", "y"], ["✗", "n"], ["✗", "n"], ["✗", "n"], ["✗", "n"]],
    ["Cross-rate calc shown", ["✓", "y"], ["✗", "n"], ["✗", "n"], ["✗", "n"], ["✗", "n"]],
    ["ZAR-native billing", ["✓", "y"], ["✗", "n"], ["✗", "n"], ["✗", "n"], ["✗", "n"]],
    ["Open-source · self-host", ["✓", "y"], ["✗", "n"], ["✗", "n"], ["✗", "n"], ["✗", "n"]],
  ],
};

export default function Pricing() {
  return (
    <div className="doc">
      <Reveal as="header" className="hero" style={{ gap: 16 }}>
        <Eyebrow>openrate Cloud</Eyebrow>
        <h1 className="display d1">Cheap, fair, <span className="accent-word">scalable</span> pricing.</h1>
        <p className="prose">
          The engine is free and open-source — <b>self-host it forever at R0</b>. openrate Cloud
          is the managed endpoint: API keys, real-time paid feeds, history and an SLA, billed
          simply by requests. Open data stays free; you only pay for genuinely-costlier capability.
        </p>
      </Reveal>

      <Reveal className="section">
        <div className="tier-grid">
          {TIERS.map((t) => (
            <div className={`tier ${t.popular ? "pop" : ""}`} key={t.name}>
              {t.popular && <span className="tier-badge">Most popular</span>}
              <div className="tier-name">{t.name}</div>
              <div className="tier-price"><span className="tp-zar">{t.zar}</span>{t.usd && <span className="tp-usd">{t.usd}/mo</span>}</div>
              <div className="tier-reqs">{t.reqs} · {t.rl}</div>
              <ul className="tier-feat">{t.feat.map((f, i) => <li key={i}>{f}</li>)}</ul>
              <a className={`tier-cta ${t.popular ? "primary" : ""}`} href="https://vulos.org" target="_blank" rel="noreferrer">{t.cta}</a>
            </div>
          ))}
        </div>
        <p className="board-hint" style={{ textAlign: "center" }}>Annual prepay −20% · overage billed, never throttled · prices in ZAR (Paystack), USD shown at R18.50/$.</p>
      </Reveal>

      <Reveal className="section">
        <div className="sec-head">
          <Eyebrow>Get started</Eyebrow>
          <h2 className="display d2">Your first request in 3 steps</h2>
        </div>
        <div className="steps">
          <Step n="1" t="Create a key">Sign in to <b>Vulos Cloud</b>, open openrate, and generate an API key. Self-hosting? Skip this — the binary needs no key.</Step>
          <Step n="2" t="Send the header">Pass it as a Bearer token on every request. Free keys are scoped to the open daily sources.</Step>
          <Step n="3" t="Read the grade">Each response carries the rate, the per-leg calculation, sources and the quality grade.</Step>
        </div>
        <CodeBlock title="api.openrate.dev/api/v1/convert?from=USD&to=ZAR&amount=100" code={`$ curl https://api.openrate.dev/api/v1/convert?from=USD&to=ZAR&amount=100 \\
    -H "Authorization: Bearer or_live_•••••••••••••"

{
  "result": 1639.47,
  "rate": {
    "rate": 16.3947,
    "legs": [ { "from": "USD", "to": "ZAR", "rate": 16.3947, "source": "coinbase" } ],
    "quality": { "grade": "B", "confidence": 0.89, "source_class": "exchange" }
  }
}`} />
      </Reveal>

      <Reveal className="section">
        <div className="sec-head">
          <Eyebrow>Grounded comparison</Eyebrow>
          <h2 className="display d2">How openrate compares</h2>
          <p className="board-hint">Public entry prices &amp; free tiers, checked June 2026. The real difference is what's in the response.</p>
        </div>
        <div className="cmp-wrap">
          <table className="cmp">
            <thead>
              <tr><th></th>{COMPARE.cols.map((c, i) => <th key={c} className={i === 0 ? "us" : ""}>{c}</th>)}</tr>
            </thead>
            <tbody>
              {COMPARE.rows.map((row) => (
                <tr key={row[0]}>
                  <td className="cmp-feat">{row[0]}</td>
                  {row.slice(1).map((cell, i) => (
                    <td key={i} className={`${i === 0 ? "us" : ""} ${cell[1] === "y" ? "yes" : cell[1] === "n" ? "no" : ""}`}>{cell[0]}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="board-hint">Every competitor returns a bare number. openrate returns the number <b>plus its grade, the cross-source dispersion, and the full cross-rate calculation</b> — so you know how much to trust it.</p>
      </Reveal>
    </div>
  );
}

function Step({ n, t, children }) {
  return (
    <div className="step">
      <span className="step-n">{n}</span>
      <div><b>{t}</b><p>{children}</p></div>
    </div>
  );
}
