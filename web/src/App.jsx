import React, { useEffect, useMemo, useState } from "react";
import { getMeta, getRates, convert, ageLabel } from "./api.js";
import Accuracy from "./Accuracy.jsx";

const GRADE_CLASS = { A: "bA", B: "bB", C: "bC", D: "bD" };

export function Grade({ q, size = "sm" }) {
  if (!q) return null;
  return (
    <span
      className={`grade ${size} ${GRADE_CLASS[q.grade] || ""}`}
      title={`confidence ${(q.confidence * 100).toFixed(0)}% · ${q.freshness} · ${q.directness} · ${q.source_class} source · ${q.corroboration.sources} corroborating`}
    >
      {q.grade}
    </span>
  );
}

export default function App() {
  const [tab, setTab] = useState("convert");
  const [meta, setMeta] = useState(null);
  const [base, setBase] = useState("ZAR");
  const [rates, setRates] = useState(null);
  const [err, setErr] = useState(null);

  useEffect(() => { getMeta().then(setMeta).catch((e) => setErr(e.message)); }, []);
  useEffect(() => { getRates(base).then(setRates).catch((e) => setErr(e.message)); }, [base]);

  const currencies = meta?.currencies ?? [];

  return (
    <>
      <nav className="nav">
        <div className="brand">
          <img src="/openrate.svg" alt="" className="logo" />
          <span className="name">open<b>rate</b></span>
        </div>
        <span className="pill">open · {base}-anchored</span>
        <div className="spacer" />
        <div className="tabs">
          <button className={tab === "convert" ? "on" : ""} onClick={() => setTab("convert")}>Convert</button>
          <button className={tab === "accuracy" ? "on" : ""} onClick={() => setTab("accuracy")}>Accuracy</button>
        </div>
      </nav>

      <div className="wrap">
        <header className="hero">
          <h1>Exchange rates, <span className="accent">graded for accuracy</span>.</h1>
          <p>
            An open, {base}-anchored rate engine. Rates come from central banks and live
            venues — never a paid API — and every price ships with a quality grade so you
            know exactly how much to trust it.
          </p>
        </header>

        {err && <div className="err">{err}</div>}

        {tab === "accuracy" ? (
          <Accuracy />
        ) : (
          <>
            <Converter currencies={currencies} defaultFrom="USD" defaultTo={base} />
            <section className="card">
              <div className="row between">
                <h2>Rates · 1 {base} =</h2>
                <label className="baselbl">
                  base
                  <select value={base} onChange={(e) => setBase(e.target.value)}>
                    {currencies.map((c) => <option key={c} value={c}>{c}</option>)}
                  </select>
                </label>
              </div>
              <RatesTable rates={rates?.rates} />
              {rates && (
                <p className="foot">
                  built {new Date(rates.built_at).toLocaleString()} · sources:{" "}
                  {(meta?.sources ?? []).map((s) => `${s.name}(${s.edges})`).join(", ")}
                </p>
              )}
            </section>
          </>
        )}
      </div>
    </>
  );
}

function Converter({ currencies, defaultFrom, defaultTo }) {
  const [from, setFrom] = useState(defaultFrom);
  const [to, setTo] = useState(defaultTo);
  const [amount, setAmount] = useState(100);
  const [out, setOut] = useState(null);

  useEffect(() => { setTo(defaultTo); }, [defaultTo]);
  useEffect(() => {
    if (!from || !to) return;
    convert(from, to, amount).then(setOut).catch(() => setOut(null));
  }, [from, to, amount]);

  const q = out?.rate?.quality;
  return (
    <section className="card conv">
      <div className="conv-grid">
        <input type="number" value={amount} onChange={(e) => setAmount(e.target.value)} aria-label="amount" />
        <Select value={from} onChange={setFrom} options={currencies} />
        <button className="swap" title="swap" onClick={() => { setFrom(to); setTo(from); }}>⇄</button>
        <Select value={to} onChange={setTo} options={currencies} />
        <span />
      </div>
      {out && (
        <div className="result">
          <div className="resline">
            <strong>{Number(out.result).toLocaleString(undefined, { maximumFractionDigits: 4 })}</strong>
            <span className="unit">{out.to}</span>
            <Grade q={q} size="lg" />
          </div>
          {q && (
            <div className="prov">
              rate {Number(out.rate.rate).toPrecision(6)} · {out.rate.hops} hop{out.rate.hops === 1 ? "" : "s"} · {ageLabel(out.rate.age_sec)} · via {out.rate.path.join(" → ")}
              <div className="qbits">
                <Bit label="confidence" v={`${(q.confidence * 100).toFixed(0)}%`} />
                <Bit label="freshness" v={q.freshness} />
                <Bit label="directness" v={q.directness.replace("_", " ")} />
                <Bit label="source" v={q.source_class} />
                <Bit label="corroboration" v={`${q.corroboration.sources} src${q.corroboration.sources > 1 ? ` · ${q.corroboration.spread_bps}bps` : ""}`} />
              </div>
              {q.caveats?.length > 0 && (
                <ul className="caveats">{q.caveats.map((c, i) => <li key={i}>⚠ {c}</li>)}</ul>
              )}
            </div>
          )}
        </div>
      )}
    </section>
  );
}

function Bit({ label, v }) {
  return <span className="bit"><em>{label}</em>{v}</span>;
}

function Select({ value, onChange, options }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)} aria-label="currency">
      {options.map((c) => <option key={c} value={c}>{c}</option>)}
    </select>
  );
}

function RatesTable({ rates }) {
  const rows = useMemo(() => {
    if (!rates) return [];
    return Object.entries(rates).sort(([a], [b]) => a.localeCompare(b));
  }, [rates]);

  if (!rates) return <p className="muted">loading…</p>;

  return (
    <table className="rates">
      <thead>
        <tr><th>CCY</th><th className="num">Rate</th><th>Grade</th><th>Freshness</th><th>Source</th></tr>
      </thead>
      <tbody>
        {rows.map(([ccy, p]) => (
          <tr key={ccy}>
            <td className="ccy">{ccy}</td>
            <td className="num">{Number(p.rate).toPrecision(6)}</td>
            <td><Grade q={p.quality} /></td>
            <td className="muted">{ageLabel(p.age_sec)}</td>
            <td className="muted">{p.quality?.source_class}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
