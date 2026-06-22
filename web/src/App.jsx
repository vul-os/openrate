import React, { useEffect, useMemo, useState } from "react";
import { getMeta, getRates, convert, ageLabel } from "./api.js";

export default function App() {
  const [meta, setMeta] = useState(null);
  const [base, setBase] = useState("ZAR");
  const [rates, setRates] = useState(null);
  const [err, setErr] = useState(null);

  useEffect(() => {
    getMeta().then(setMeta).catch((e) => setErr(e.message));
  }, []);

  useEffect(() => {
    getRates(base).then(setRates).catch((e) => setErr(e.message));
  }, [base]);

  const currencies = meta?.currencies ?? [];

  return (
    <div className="wrap">
      <header className="hd">
        <img src="/openrate.svg" alt="" className="logo" />
        <div>
          <h1>openrate</h1>
          <p className="tag">Open, {base}-anchored exchange rates — a graph of source-native quotes, no single base.</p>
        </div>
      </header>

      {err && <div className="err">{err}</div>}

      <Converter currencies={currencies} defaultFrom="USD" defaultTo={base} />

      <section className="card">
        <div className="row between">
          <h2>Rates · 1 {base} =</h2>
          <label className="baselbl">
            base&nbsp;
            <select value={base} onChange={(e) => setBase(e.target.value)}>
              {currencies.map((c) => (
                <option key={c} value={c}>{c}</option>
              ))}
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
          <strong>{Number(out.result).toLocaleString(undefined, { maximumFractionDigits: 4 })}</strong> {out.to}
          <span className="prov">
            rate {Number(out.rate.rate).toPrecision(6)} · {out.rate.hops} hop{out.rate.hops === 1 ? "" : "s"}
            {" · "}{ageLabel(out.rate.age_sec)} · via {out.rate.path.join("→")}
          </span>
        </div>
      )}
    </section>
  );
}

function Select({ value, onChange, options }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)}>
      {options.map((c) => (
        <option key={c} value={c}>{c}</option>
      ))}
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
        <tr><th>CCY</th><th className="num">Rate</th><th>Hops</th><th>Freshness</th></tr>
      </thead>
      <tbody>
        {rows.map(([ccy, p]) => (
          <tr key={ccy}>
            <td className="ccy">{ccy}</td>
            <td className="num">{Number(p.rate).toPrecision(6)}</td>
            <td><span className={`hop h${Math.min(p.hops, 3)}`}>{p.hops}</span></td>
            <td className="muted">{ageLabel(p.age_sec)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
