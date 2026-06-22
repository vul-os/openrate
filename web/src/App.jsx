import React, { useEffect, useMemo, useState } from "react";
import { getMeta, getRates, convert, ageLabel } from "./api.js";
import { Reveal, Eyebrow, ThemeToggle } from "./ui.jsx";
import CurrencySelect from "./CurrencySelect.jsx";
import Footer from "./Footer.jsx";
import { ccyFlag } from "./currencies.js";
import Accuracy from "./Accuracy.jsx";

const GRADE_CLASS = { A: "bA", B: "bB", C: "bC", D: "bD" };
const fmt = (n, d = 4) => Number(n).toLocaleString(undefined, { maximumFractionDigits: d });

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
        <div className="spacer" />
        <div className="switch">
          <button className={tab === "convert" ? "on" : ""} onClick={() => setTab("convert")}>Convert</button>
          <button className={tab === "accuracy" ? "on" : ""} onClick={() => setTab("accuracy")}>Accuracy</button>
        </div>
        <ThemeToggle />
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
                Built the open way — central banks and live venues, never a paid API. Every
                price ships with a quality grade and the stats behind it, so you know exactly
                how much to trust it.
              </p>
              <div className="hero-controls">
                <div className="anchor-ctl">
                  <span className="anchor-lbl">Anchored to</span>
                  <CurrencySelect compact value={base} onChange={setBase} options={currencies} />
                </div>
                <div className="stats">
                  <Stat v={currencies.length || "—"} l="currencies" />
                  <Stat v={liveSources || "—"} l="live sources" accent />
                  <Stat v="~1 min" l="freshness" />
                </div>
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
                <p className="prose" style={{ fontSize: "0.95rem" }}>Click any row for the calculation, sources and dispersion stats.</p>
              </div>
              <div className="card">
                <RatesTable rates={rates?.rates} base={base} />
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
      <Footer meta={meta} base={base} />
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
  const [showMath, setShowMath] = useState(false);

  useEffect(() => { setTo(defaultTo); }, [defaultTo]);
  useEffect(() => {
    if (!from || !to) return;
    convert(from, to, amount).then(setOut).catch(() => setOut(null));
  }, [from, to, amount]);

  const rate = out?.rate;
  const q = rate?.quality;
  const r = rate?.rate;
  const QUICK = [1, 10, 100, 1000, 10000];

  return (
    <section className="conv">
      <div className="conv-grid">
        <div className="field">
          <label>Amount</label>
          <input type="number" value={amount} onChange={(e) => setAmount(e.target.value)} />
        </div>
        <CurrencySelect label="From" value={from} onChange={setFrom} options={currencies} />
        <div className="swap-wrap"><button className="swap" title="swap" onClick={() => { setFrom(to); setTo(from); }}>⇄</button></div>
        <CurrencySelect label="To" value={to} onChange={setTo} options={currencies} />
      </div>

      <div className="quick">
        {QUICK.map((n) => (
          <button key={n} className={`chip ${Number(amount) === n ? "on" : ""}`} onClick={() => setAmount(n)}>{n.toLocaleString()}</button>
        ))}
      </div>

      {out && rate && (
        <div className="result">
          <div className="resline">
            <div className="res-main">
              <strong>{fmt(out.result)}</strong>
              <span className="unit">{out.to}</span>
            </div>
            <Grade q={q} size="lg" />
          </div>
          <div className="inverse">
            <span>1 {from} = {fmt(r, 6)} {to}</span>
            <span className="sep">·</span>
            <span>1 {to} = {fmt(1 / r, 6)} {from}</span>
          </div>
          {q && (
            <>
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
              <button className="math-toggle" onClick={() => setShowMath((s) => !s)}>
                {showMath ? "▾ hide the math" : "▸ show the math"}
              </button>
              {showMath && <Math rate={rate} from={from} to={to} />}
            </>
          )}
        </div>
      )}
    </section>
  );
}

function Bit({ label, v }) {
  return <span className="bit"><em>{label}</em>{v}</span>;
}

// Math — transparent derivation + cross-source financial stats for a rate view.
function Math({ rate, from, to }) {
  const c = rate.quality.corroboration;
  const quotes = (rate.quotes || []).slice().sort((a, b) => a.rate - b.rate);
  const lo = quotes.length ? quotes[0].rate : 0;
  const hi = quotes.length ? quotes[quotes.length - 1].rate : 1;
  const span = hi - lo || 1;
  return (
    <div className="math">
      <div className="math-row">
        <span className="ml">calculation</span>
        <span className="mv">
          {rate.hops <= 1 ? "directly quoted" : `${rate.hops}-hop cross-rate`} · path {rate.path.join(" → ")} · chosen <b>{rate.sources.join(", ")}</b>
        </span>
      </div>
      <div className="math-row">
        <span className="ml">accuracy</span>
        <span className="mv">grade <b>{rate.quality.grade}</b> · {(rate.quality.confidence * 100).toFixed(0)}% confidence · {rate.quality.freshness} · {rate.quality.source_class}</span>
      </div>

      {quotes.length > 0 && (
        <>
          <div className="math-row"><span className="ml">sources</span><span className="mv">{quotes.length} quoting {from}→{to} directly</span></div>
          <div className="quotes">
            {quotes.map((qq) => (
              <div className="qrow" key={qq.source}>
                <span className="qsrc">{qq.source}</span>
                <span className="qbar"><span className="qfill" style={{ left: `${((qq.rate - lo) / span) * 100}%` }} /></span>
                <span className="qrate">{fmt(qq.rate, 6)}</span>
                <span className="qage muted">{Math.round(qq.age_sec)}s</span>
              </div>
            ))}
          </div>
        </>
      )}

      {c.sources > 1 ? (
        <div className="stats-grid">
          <Stt l="mean" v={fmt(c.mean, 6)} />
          <Stt l="std dev" v={`${c.stdev_bps} bps`} />
          <Stt l="min–max" v={`${fmt(c.min, 5)} – ${fmt(c.max, 5)}`} />
          <Stt l="spread" v={`${c.spread_bps} bps`} accent={c.spread_bps > 50} />
        </div>
      ) : (
        <p className="math-note">Single direct source — no cross-source dispersion. Add a paid source (see <code>.env.example</code>) for corroboration.</p>
      )}
    </div>
  );
}

function Stt({ l, v, accent }) {
  return <div className={`stt ${accent ? "warn" : ""}`}><span className="stt-l">{l}</span><span className="stt-v">{v}</span></div>;
}

function RatesTable({ rates, base }) {
  const [openCcy, setOpenCcy] = useState(null);
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
          <React.Fragment key={ccy}>
            <tr className={`rrow ${openCcy === ccy ? "open" : ""}`} onClick={() => setOpenCcy(openCcy === ccy ? null : ccy)}>
              <td className="ccy"><span className="rflag">{ccyFlag(ccy)}</span> {ccy}<span className="rcaret">{openCcy === ccy ? "▾" : "▸"}</span></td>
              <td className="num">{Number(p.rate).toPrecision(6)}</td>
              <td><Grade q={p.quality} /></td>
              <td className="muted">{ageLabel(p.age_sec)}</td>
              <td className="muted">{p.quality?.source_class}</td>
            </tr>
            {openCcy === ccy && (
              <tr className="rdetail"><td colSpan={5}><Math rate={p} from={base} to={ccy} /></td></tr>
            )}
          </React.Fragment>
        ))}
      </tbody>
    </table>
  );
}
