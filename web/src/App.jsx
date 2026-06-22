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
    <span className={`grade ${size} ${GRADE_CLASS[q.grade] || ""}`}
      title={`confidence ${(q.confidence * 100).toFixed(0)}% · ${q.freshness} · ${q.directness} · ${q.source_class} source · ${q.corroboration.sources} corroborating`}>
      {q.grade}
    </span>
  );
}

export default function App() {
  const [tab, setTabState] = useState(() => (location.hash === "#accuracy" ? "accuracy" : "convert"));
  const setTab = (t) => { setTabState(t); history.replaceState(null, "", t === "accuracy" ? "#accuracy" : "#"); };
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
        <div className="nav-anchor">
          <span className="anchor-lbl">Anchor</span>
          <CurrencySelect compact value={base} onChange={setBase} options={currencies} />
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
                <p className="board-hint">Click any row for the calculation, sources and dispersion stats.</p>
              </div>
              <div className="card pad0">
                <RatesTable rates={rates?.rates} base={base} />
                {rates && (
                  <p className="foot">
                    built {new Date(rates.built_at).toLocaleString()} · {(meta?.sources ?? []).map((s) => `${s.name}(${s.edges})`).join(" · ")}
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
  const swap = () => { setFrom(to); setTo(from); };

  return (
    <section className="conv">
      <div className="conv-stack">
        <div className="cfield">
          <label>You convert</label>
          <div className="cf-row">
            <input className="cf-amount" type="number" value={amount} onChange={(e) => setAmount(e.target.value)} />
            <CurrencySelect value={from} onChange={setFrom} options={currencies} />
          </div>
        </div>

        <div className="conv-divider"><button className="swap-mid" title="swap" onClick={swap}>⇄</button></div>

        <div className="cfield">
          <div className="cf-head"><label>You get</label>{q && <span className="cf-grade"><Grade q={q} size="lg" /></span>}</div>
          <div className="cf-row">
            <div className="cf-result">{out ? fmt(out.result) : "—"}</div>
            <CurrencySelect value={to} onChange={setTo} options={currencies} />
          </div>
        </div>
      </div>

      <div className="quick">
        {QUICK.map((n) => (
          <button key={n} className={`chip ${Number(amount) === n ? "on" : ""}`} onClick={() => setAmount(n)}>{n.toLocaleString()}</button>
        ))}
      </div>

      {out && rate && q && (
        <div className="conv-detail">
          <div className="inverse">
            <span>1 {from} = <b>{fmt(r, 6)}</b> {to}</span>
            <span className="sep">·</span>
            <span>1 {to} = <b>{fmt(1 / r, 6)}</b> {from}</span>
          </div>

          <div className="qtiles">
            <QT l="grade" v={q.grade} grade={GRADE_CLASS[q.grade]} />
            <QT l="confidence" v={`${(q.confidence * 100).toFixed(0)}%`} />
            <QT l="freshness" v={q.freshness} />
            <QT l="directness" v={q.directness.replace("_", " ")} />
            <QT l="source" v={q.source_class} />
            <QT l="corroboration" v={`${q.corroboration.sources}${q.corroboration.sources > 1 ? ` · ${q.corroboration.spread_bps}bps` : ""}`} />
          </div>

          {q.caveats?.length > 0 && (
            <ul className="caveats">{q.caveats.map((c, i) => <li key={i}><span>⚠</span><span>{c}</span></li>)}</ul>
          )}

          <button className="math-toggle" onClick={() => setShowMath((s) => !s)}>
            {showMath ? "▾ hide the math" : "▸ show the math"}
          </button>
          {showMath && <Calc rate={rate} from={from} to={to} />}
        </div>
      )}
    </section>
  );
}

function QT({ l, v, grade }) {
  return <div className="qt"><span className="qt-l">{l}</span><span className={`qt-v ${grade ? "g " + grade : ""}`}>{v}</span></div>;
}

// Calc — transparent derivation + cross-source financial stats for a rate view.
function Calc({ rate, from, to }) {
  const c = rate.quality.corroboration;
  const quotes = (rate.quotes || []).slice().sort((a, b) => a.rate - b.rate);
  const lo = quotes.length ? quotes[0].rate : 0;
  const hi = quotes.length ? quotes[quotes.length - 1].rate : 1;
  const span = hi - lo || 1;
  const legs = rate.legs || [];
  return (
    <div className="math">
      <div className="math-sec">
        Calculation<span className="sec-tag">{rate.hops <= 1 ? "directly quoted" : `${rate.hops}-hop cross-rate`}</span>
      </div>
      <div className="legs">
        {legs.map((l, i) => (
          <div className="leg" key={i}>
            <span className="leg-pair"><b>{l.from}</b> → <b>{l.to}</b></span>
            <span className="leg-rate">{fmt(l.rate, 6)}</span>
            <span className="leg-src">{l.source} · {Math.round(l.age_sec)}s</span>
          </div>
        ))}
        {legs.length > 1 && (
          <div className="leg leg-total">
            <span className="leg-pair"><b>{from}</b> → <b>{to}</b></span>
            <span className="leg-rate">= {fmt(rate.rate, 6)}</span>
            <span className="leg-calc">{legs.map((l) => fmt(l.rate, 4)).join(" × ")}</span>
          </div>
        )}
      </div>

      {quotes.length > 0 && (
        <>
          <div className="math-sec">Sources · {quotes.length} quoting {from}→{to}</div>
          <div className="quotes">
            {quotes.map((qq) => (
              <div className="qrow" key={qq.source}>
                <span className="qsrc">{qq.source}</span>
                <span className="qbar"><span className="qfill" style={{ left: `${((qq.rate - lo) / span) * 100}%` }} /></span>
                <span className="qrate">{fmt(qq.rate, 6)}</span>
                <span className="qage">{Math.round(qq.age_sec)}s</span>
              </div>
            ))}
          </div>
        </>
      )}

      <div className="math-sec">Dispersion</div>
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

  if (!rates) return <p className="muted pad">loading…</p>;

  return (
    <table className="rates">
      <thead>
        <tr><th>Currency</th><th className="num">Rate</th><th>Grade</th><th>Freshness</th><th>Source</th></tr>
      </thead>
      <tbody>
        {rows.map(([ccy, p]) => (
          <React.Fragment key={ccy}>
            <tr className={`rrow ${openCcy === ccy ? "open" : ""}`} onClick={() => setOpenCcy(openCcy === ccy ? null : ccy)}>
              <td className="ccy"><span className="rcaret">{openCcy === ccy ? "▾" : "▸"}</span><span className="rflag">{ccyFlag(ccy)}</span>{ccy}</td>
              <td className="num">{Number(p.rate).toPrecision(6)}</td>
              <td><Grade q={p.quality} /></td>
              <td className="muted">{ageLabel(p.age_sec)}</td>
              <td className="muted">{p.quality?.source_class}</td>
            </tr>
            {openCcy === ccy && (
              <tr className="rdetail"><td colSpan={5}><Calc rate={p} from={base} to={ccy} /></td></tr>
            )}
          </React.Fragment>
        ))}
      </tbody>
    </table>
  );
}
