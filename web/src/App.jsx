import React, { useEffect, useMemo, useState } from "react";
import { getMeta, getRates, convert, ageLabel } from "./api.js";
import { Reveal, Eyebrow, ThemeToggle, useScrollSpy, GitHubIcon } from "./ui.jsx";
import CurrencySelect from "./CurrencySelect.jsx";
import Footer from "./Footer.jsx";
import { ccyFlag } from "./currencies.js";
import Accuracy from "./Accuracy.jsx";
import Pricing from "./Pricing.jsx";
import Docs from "./Docs.jsx";

const REPO = "https://github.com/vul-os/openrate";

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

const NAV = [
  { id: "convert", label: "Convert" },
  { id: "accuracy", label: "Accuracy" },
  { id: "pricing", label: "Pricing" },
];

export default function App() {
  const [meta, setMeta] = useState(null);
  const [base, setBase] = useState("ZAR");
  const [rates, setRates] = useState(null);
  const [err, setErr] = useState(null);
  const [route, setRoute] = useState(() => (location.hash.startsWith("#docs") ? "docs" : "home"));
  const active = useScrollSpy(["convert", "accuracy", "pricing"]);

  useEffect(() => { getMeta().then(setMeta).catch((e) => setErr(e.message)); }, []);
  useEffect(() => { getRates(base).then(setRates).catch((e) => setErr(e.message)); }, [base]);
  useEffect(() => {
    const onHash = () => {
      const r = location.hash.startsWith("#docs") ? "docs" : "home";
      setRoute(r);
      if (r === "docs") window.scrollTo(0, 0);
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  // home-section nav: smooth-scroll (switching back from docs first if needed)
  const go = (e, id) => {
    e.preventDefault();
    const scroll = () => { document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" }); };
    history.replaceState(null, "", id === "convert" ? "#" : `#${id}`);
    if (route !== "home") { setRoute("home"); requestAnimationFrame(() => requestAnimationFrame(scroll)); }
    else scroll();
  };
  const goDocs = (e) => { e.preventDefault(); setRoute("docs"); history.replaceState(null, "", "#docs"); window.scrollTo(0, 0); };

  const currencies = meta?.currencies ?? [];
  const liveSources = (meta?.sources ?? []).filter((s) => !s.last_error && s.edges > 0).length;

  return (
    <>
      <nav className="nav">
        <a className="brand" href="#convert" onClick={(e) => go(e, "convert")}>
          <img src="/openrate.svg" alt="open rate" className="logo" />
          <span className="name">open <b>rate</b></span>
        </a>
        <div className="nav-anchor">
          <span className="anchor-lbl">Anchor</span>
          <CurrencySelect compact value={base} onChange={setBase} options={currencies} />
        </div>
        <div className="spacer" />
        <div className="navlinks">
          {NAV.map((n) => (
            <a key={n.id} href={`#${n.id}`} className={route === "home" && active === n.id ? "on" : ""} onClick={(e) => go(e, n.id)}>{n.label}</a>
          ))}
          <a href="#docs" className={route === "docs" ? "on" : ""} onClick={goDocs}>Docs</a>
        </div>
        <a className="nav-icon" href={REPO} target="_blank" rel="noreferrer" title="GitHub" aria-label="GitHub"><GitHubIcon size={18} /></a>
        <a className="nav-cta" href="#pricing" onClick={(e) => go(e, "pricing")}>Get API key</a>
        <ThemeToggle />
      </nav>

      {route === "docs" ? (
        <main><div className="docs-page"><Docs /></div></main>
      ) : (
      <main>
        <section id="convert" className="sect">
          <div className="wrap">
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
          </div>
        </section>

        <section id="accuracy" className="sect alt">
          <div className="wrap"><Accuracy /></div>
        </section>

        <section id="pricing" className="sect">
          <div className="wrap"><Pricing /></div>
        </section>
      </main>
      )}
      <Footer meta={meta} base={base} onDocs={goDocs} />
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
