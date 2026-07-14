import React from "react";
import VulosMark from "./VulosMark.jsx";
import { GitHubIcon } from "./ui.jsx";

const REPO = "https://github.com/vul-os/openrate";

// Footer — openrate is part of the Vulos ecosystem; the brand column carries the
// real Vulos mark and a link home. status dot reflects the live engine.
export default function Footer({ meta, base, onDocs }) {
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
            <div className="foot-tags">
              <a className="foot-vulos" href="https://vulos.org" target="_blank" rel="noreferrer">
                <VulosMark size={18} /><span>part of <b>Vulos</b></span>
              </a>
              <a className="foot-gh" href={REPO} target="_blank" rel="noreferrer" aria-label="GitHub"><GitHubIcon size={16} /><span>GitHub</span></a>
            </div>
          </div>

          <FootCol head="Product" links={[["Docs", "#docs", onDocs], ["Pricing", "#pricing"], ["API reference", "#docs", onDocs], ["Self-host", "#docs", onDocs]]} />
          <FootCol head="Open data" links={[["ECB", "https://www.ecb.europa.eu"], ["SARB", "https://www.resbank.co.za"], ["Coinbase", "https://www.coinbase.com"], ["Luno", "https://www.luno.com"]]} />
          <FootCol head="Project" links={[["GitHub", REPO], ["SOURCES.md", `${REPO}/blob/main/SOURCES.md`], ["License (MIT)", `${REPO}/blob/main/LICENSE`], ["Third-party licences", "/licenses.txt"], ["Vulos", "https://vulos.org"]]} />
        </div>

        <div className="foot-bottom">
          <span className="foot-copy">
            © 2026 Vulos contributors · MIT licensed ·{" "}
            <a href={REPO} target="_blank" rel="noreferrer">source</a>
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
        {links.map(([t, h, onClick]) => (
          <li key={t}>
            <a href={h} onClick={onClick} target={h.startsWith("http") ? "_blank" : undefined} rel="noreferrer">{t}</a>
          </li>
        ))}
      </ul>
    </div>
  );
}
