import React, { useEffect, useMemo, useState } from "react";
import { getMeta, getRates, convert, ageLabel } from "./api.js";
import Accuracy from "./Accuracy.jsx";

const GRADE_COLOR = { A: "#2dd4bf", B: "#a5b4fc", C: "#fbbf24", D: "#fca5a5" };

export function Grade({ q, size = "sm" }) {
  if (!q) return null;
  return (
    <span
      className={`grade ${size}`}
      style={{ background: (GRADE_COLOR[q.grade] || "#888") + "22", color: GRADE_COLOR[q.grade] || "#888", borderColor: (GRADE_COLOR[q.grade] || "#888") + "55" }}
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
    <div className="wrap">
      <header className="hd">
        <img src="/openrate.svg" alt="" className="logo" />
        <div className="grow">
          <h1>openrate</h1>
          <p className="tag">Open, {base}-anchored exchange rates — every price graded for accuracy.</p>
        </div>
        <nav className="tabs">
          <button className={tab === "convert" ? "on" : ""} onClick={() => setTab("convert")}>Convert</button>
          <button className={tab === "accuracy" ? "on" : ""} onClick={() => setTab("accuracy")}>Accuracy</button>
        </nav>
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
                base&nbsp;
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
        <input type="number" value={amount} onChange={(e) => setAmount(e.target.value)} />
        <Select value={from} onChange={setFrom} options={currencies} />
        <span className="arrow">→</span>
        <Select value={to} onChange={setTo} options={currencies} />
      </div>
      {out && (
        <div className="result">
          <div className="resline">
            <strong>{Number(out.result).toLocaleString(undefined, { maximumFractionDigits: 4 })}</strong> {out.to}
            <Grade q={q} size="lg" />
          </div>
          {q && (
            <div className="prov">
              rate {Number(out.rate.rate).toPrecision(6)} · {out.rate.hops} hop{out.rate.hops === 1 ? "" : "s"} · {ageLabel(out.rate.age_sec)} · via {out.rate.path.join("→")}
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
  return <span className="bit"><em>{label}</em> {v}</span>;
}

function Select({ value, onChange, options }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)}>
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
