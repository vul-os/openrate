import React, { useEffect, useState } from "react";
import { getMeta, getRates } from "./api.js";
import { Reveal, Eyebrow, Label, ThemeToggle, useScrollSpy, Icon, Mark } from "./ui.jsx";
import Guilloche from "./Guilloche.jsx";
import CurrencySelect from "./CurrencySelect.jsx";
import Converter from "./Converter.jsx";
import Board from "./Board.jsx";
import Policy from "./Policy.jsx";
import Accuracy from "./Accuracy.jsx";
import Docs from "./Docs.jsx";
import Footer from "./Footer.jsx";

const REPO = "https://github.com/vul-os/openrate";
const SECTIONS = ["convert", "board", "policy", "accuracy"];
const NAV = [
  { id: "convert", label: "Convert" },
  { id: "board", label: "Board" },
  { id: "policy", label: "Policy" },
  { id: "accuracy", label: "Accuracy" },
];

export default function App() {
  const [meta, setMeta] = useState(null);
  const [base, setBase] = useState("ZAR");
  const [rates, setRates] = useState(null);
  const [err, setErr] = useState(null);
  const [route, setRoute] = useState(() => (location.hash.startsWith("#docs") ? "docs" : "home"));
  const active = useScrollSpy(SECTIONS);

  useEffect(() => {
    getMeta()
      .then((m) => { setMeta(m); if (m.default_base) setBase(m.default_base); })
      .catch((e) => setErr(e.message));
  }, []);

  useEffect(() => {
    let live = true;
    getRates(base)
      .then((r) => { if (live) { setRates(r); setErr(null); } })
      .catch((e) => { if (live) setErr(e.message); });
    return () => { live = false; };
  }, [base]);

  useEffect(() => {
    const onHash = () => {
      const r = location.hash.startsWith("#docs") ? "docs" : "home";
      setRoute(r);
      if (r === "docs") window.scrollTo(0, 0);
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  // Section links have to work from the docs route too: switch back, then wait
  // two frames for the home tree to mount before scrolling to the anchor.
  const go = (e, id) => {
    e.preventDefault();
    const scroll = () => document.getElementById(id)?.scrollIntoView({ behavior: "smooth", block: "start" });
    history.replaceState(null, "", id === "convert" ? "#" : `#${id}`);
    if (route !== "home") { setRoute("home"); requestAnimationFrame(() => requestAnimationFrame(scroll)); }
    else scroll();
  };
  const goDocs = (e) => {
    e.preventDefault();
    setRoute("docs");
    history.replaceState(null, "", "#docs");
    window.scrollTo(0, 0);
  };

  const currencies = meta?.currencies ?? [];
  const sources = meta?.sources ?? [];
  const liveSources = sources.filter((s) => !s.last_error && s.edges > 0).length;
  const edges = sources.reduce((n, s) => n + (s.edges || 0), 0);

  return (
    <>
      <nav className="nav">
        <a className="brand" href="#convert" onClick={(e) => go(e, "convert")}>
          <Mark size={26} />
          <span className="word">open<i>rate</i></span>
        </a>

        <div className="nav-anchor">
          <Label>Anchor</Label>
          <CurrencySelect compact value={base} onChange={setBase} options={currencies} label="anchor currency" />
        </div>

        <div className="spacer" />

        <div className="navlinks">
          {NAV.map((n) => (
            <a
              key={n.id} href={`#${n.id}`}
              className={route === "home" && active === n.id ? "on" : ""}
              onClick={(e) => go(e, n.id)}
            >{n.label}</a>
          ))}
          <a href="#docs" className={route === "docs" ? "on" : ""} onClick={goDocs}>Docs</a>
        </div>

        <a className="icon-btn plain" href={REPO} target="_blank" rel="noreferrer" title="GitHub" aria-label="GitHub">
          <Icon.GitHub />
        </a>
        <a className="nav-cta" href="#docs" onClick={goDocs}>Self-host<Icon.Arrow /></a>
        <ThemeToggle />
      </nav>

      {route === "docs" ? (
        <main><Docs /></main>
      ) : (
        <main>
          {/* ── hero + the instrument ───────────────────────────────── */}
          <section id="convert" className="sect tight">
            <Guilloche className="hero-rose" />
            <div className="wrap">
              <div className="hero">
                <Reveal className="hero-copy">
                  <Eyebrow>Open exchange-rate engine</Eyebrow>
                  <h1 className="display d1">
                    Every rate,<br />
                    <em>with its receipts</em>.
                  </h1>
                  <p className="prose">
                    Rates built the open way — central-bank reference files and live public
                    venues, never a resold paid API. Each price ships with the path it took
                    through the currency graph, the sources behind it, how far apart they
                    are, and a grade for the lot.
                  </p>
                  <div className="gauges">
                    <Gauge v={currencies.length || "—"} l="currencies" />
                    <Gauge v={liveSources || "—"} l="sources live" live />
                    <Gauge v={edges || "—"} l="graph edges" />
                    <Gauge v={rates ? Object.keys(rates.rates || {}).length : "—"} l="pairs served" />
                  </div>
                </Reveal>

                <Reveal delay={90}>
                  <Converter currencies={currencies} defaultFrom="USD" defaultTo={base} />
                </Reveal>
              </div>

              {err && (
                <div className="err" style={{ marginTop: 32 }}>
                  <Icon.Warn style={{ flex: "none", marginTop: 3 }} />
                  <span>
                    <b>The engine isn't answering.</b> {err} — if you are running this
                    yourself, check that <code>openrate</code> is up and has finished its
                    first refresh.
                  </span>
                </div>
              )}
            </div>
          </section>

          {/* ── the board ───────────────────────────────────────────── */}
          <section id="board" className="sect ruled">
            <div className="wrap">
              <Reveal className="sec-head">
                <Eyebrow>Live board</Eyebrow>
                <h2 className="display d2">All {currencies.length || ""} currencies, <em>graded</em>.</h2>
                <p className="prose">
                  Sort by grade or age to find the numbers you shouldn't lean on. Open any
                  row for the walk through the graph and the source-by-source spread.
                </p>
              </Reveal>
              <Reveal delay={60}>
                <Board rates={rates?.rates} base={base} meta={meta} builtAt={rates?.built_at} />
              </Reveal>
            </div>
          </section>

          {/* ── policy rates ────────────────────────────────────────── */}
          <section id="policy" className="sect sunk">
            <div className="wrap">
              <Reveal className="sec-head">
                <Eyebrow>Central banks</Eyebrow>
                <h2 className="display d2">Policy rates, <em>same contract</em>.</h2>
                <p className="prose">
                  The same binary carries a second engine for central-bank policy rates —
                  flat series rather than a currency graph, from the BIS and SARB, graded on
                  the identical A–D scale and dated so an old observation can't pass for a
                  current one.
                </p>
              </Reveal>
              <Reveal delay={60}><Policy /></Reveal>
            </div>
          </section>

          {/* ── accuracy ────────────────────────────────────────────── */}
          <section id="accuracy" className="sect ruled">
            <div className="wrap"><Accuracy /></div>
          </section>
        </main>
      )}

      <Footer meta={meta} base={base} onDocs={goDocs} />
    </>
  );
}

function Gauge({ v, l, live }) {
  return (
    <div className={`gauge ${live ? "live" : ""}`}>
      <span className="g-v">{typeof v === "number" ? v.toLocaleString() : v}</span>
      <Label>{l}</Label>
    </div>
  );
}
