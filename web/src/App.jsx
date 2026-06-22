import React, { useEffect, useMemo, useState } from "react";
import { getMeta, getRates, convert, ageLabel } from "./api.js";
import { Reveal, Eyebrow } from "./ui.jsx";
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
  const liveSources = (meta?.sources ?? []).filter((s) => !s.last_error && s.edges > 0).length;

  return (
    <>
      <nav className="nav">
        <div className="brand">
          <img src="/openrate.svg" alt="" className="logo" />
          <span className="name">open<b>rate</b></span>
        </div>
        <span className="pill"><span className="dot" />open · {base}-anchored</span>
        <div className="spacer" />
        <div className="tabs">
          <button className={tab === "convert" ? "on" : ""} onClick={() => setTab("convert")}>Convert</button>
          <button className={tab === "accuracy" ? "on" : ""} onClick={() => setTab("accuracy")}>Accuracy</button>
        </div>
      </nav>

      <div className="wrap">
        {tab === "accuracy" ? (
          <Accuracy />
        ) : (
          <>
            <Reveal as="header" className="hero">
              <Eyebrow>Open exchange-rate engine</Eyebrow>
              <h1 className="display d1">Exchange rates,<br /><span className="accent-word">graded for accuracy</span>.</h1>
              <p className="prose">
                A {base}-anchored rate engine built the open way — central banks and live
                venues, never a paid API. Every price ships with a quality grade, so you
                know exactly how much to trust it.
              </p>
              <div className="stats">
                <Stat v={currencies.length || "—"} l="currencies" />
                <Stat v={liveSources || "—"} l="live sources" accent />
                <Stat v="~1 min" l="freshness" />
              </div>
            </Reveal>

            {err && <div className="err">{err}</div>}

            <Reveal className="section" delay={60}>
              <Converter currencies={currencies} defaultFrom="USD" defaultTo={base} />
            </Reveal>

            <Reveal className="section" delay={40}>
              <div className="sec-head">
                <Eyebrow>Live board</Eyebrow>
                <h2 className="display d2">All rates, 1&nbsp;{base}&nbsp;=</h2>
              </div>
              <div className="card">
                <div className="row between" style={{ marginBottom: 12 }}>
                  <span className="muted" style={{ fontSize: 13 }}>{Object.keys(rates?.rates || {}).length} currencies</span>
                  <label className="muted" style={{ fontSize: 13, display: "flex", gap: 6, alignItems: "center" }}>
                    base
                    <select value={base} onChange={(e) => setBase(e.target.value)} style={{ padding: "6px 10px", fontSize: 13 }}>
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
              </div>
            </Reveal>
          </>
        )}
      </div>
    </>
  );
}

function Stat({ v, l, accent }) {
  return (
    <div className="stat">
      <span className="v">{accent ? <em>{v}</em> : v}</span>
      <span className="l">{l}</span>
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
    <section className="conv">
      <div className="conv-grid">
        <div className="field">
          <label>Amount</label>
          <input type="number" value={amount} onChange={(e) => setAmount(e.target.value)} />
        </div>
        <Field label="From" value={from} onChange={setFrom} options={currencies} />
        <div className="swap-wrap"><button className="swap" title="swap" onClick={() => { setFrom(to); setTo(from); }}>⇄</button></div>
        <Field label="To" value={to} onChange={setTo} options={currencies} />
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
                <ul className="caveats">{q.caveats.map((c, i) => <li key={i}><span>⚠</span><span>{c}</span></li>)}</ul>
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

function Field({ label, value, onChange, options }) {
  return (
    <div className="field">
      <label>{label}</label>
      <select value={value} onChange={(e) => onChange(e.target.value)} aria-label={label}>
        {options.map((c) => <option key={c} value={c}>{c}</option>)}
      </select>
    </div>
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
