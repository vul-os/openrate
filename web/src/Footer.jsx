import React from "react";
import VulosMark from "./VulosMark.jsx";

// Footer — openrate is part of the Vulos ecosystem; the brand column carries the
// real Vulos mark and a link home. status dot reflects the live engine.
export default function Footer({ meta, base }) {
  const live = (meta?.sources ?? []).filter((s) => !s.last_error && s.edges > 0).length;
  const built = meta?.built_at ? new Date(meta.built_at) : null;
  return (
    <footer className="foot-wrap" role="contentinfo">
      <div className="foot-inner">
        <div className="foot-grid">
          <div className="foot-brand">
            <div className="foot-mark">
              <img src="/openrate.svg" width="28" height="28" alt="" />
              <span className="name">open <b>rate</b></span>
            </div>
            <p>Open, {base}-anchored exchange rates — graded for accuracy. Central banks and live venues, never a paid API.</p>
            <a className="foot-vulos" href="https://vulos.org" target="_blank" rel="noreferrer">
              <VulosMark size={18} />
              <span>part of <b>Vulos</b></span>
            </a>
          </div>

          <FootCol head="Product" links={[["Convert", "#"], ["Accuracy", "#"], ["API", "/api/v1/meta"]]} />
          <FootCol head="Open data" links={[["ECB", "https://www.ecb.europa.eu"], ["SARB", "https://www.resbank.co.za"], ["Coinbase", "https://www.coinbase.com"], ["Luno", "https://www.luno.com"]]} />
          <FootCol head="Vulos" links={[["vulos.org", "https://vulos.org"], ["Vulos Cloud", "https://vulos.org"], ["GitHub", "https://github.com/vul-os"]]} />
        </div>

        <div className="foot-bottom">
          <span className="foot-copy">
            © 2026 Vulos contributors · MIT licensed ·{" "}
            <a href="https://github.com/vul-os/openrate" target="_blank" rel="noreferrer">source</a>
          </span>
          <span className="foot-status">
            <span className="foot-dot" />
            {live ? `${live} sources live` : "starting…"}
            {built ? ` · updated ${built.toLocaleTimeString()}` : ""}
          </span>
        </div>
      </div>
    </footer>
  );
}

function FootCol({ head, links }) {
  return (
    <div className="foot-col">
      <div className="foot-col-head">{head}</div>
      <ul>
        {links.map(([t, h]) => (
          <li key={t}><a href={h} target={h.startsWith("http") ? "_blank" : undefined} rel="noreferrer">{t}</a></li>
        ))}
      </ul>
    </div>
  );
}
