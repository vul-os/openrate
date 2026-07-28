import React from "react";
import VulosMark from "./VulosMark.jsx";
import Guilloche from "./Guilloche.jsx";
import { Label, Icon, Mark } from "./ui.jsx";

const REPO = "https://github.com/vul-os/openrate";

export default function Footer({ meta, base, onDocs }) {
  const live = (meta?.sources ?? []).filter((s) => !s.last_error && s.edges > 0).length;
  const built = meta?.built_at ? new Date(meta.built_at) : null;

  return (
    <footer className="foot" role="contentinfo">
      <Guilloche className="foot-rose" seed={1} />
      <div className="foot-inner">
        <div className="foot-grid">
          <div className="foot-brand">
            <a className="brand" href="#convert">
              <Mark size={24} />
              <span className="word">open<i>rate</i></span>
            </a>
            <p>
              Open, {base}-anchored exchange rates with an accuracy grade on every price.
              Central banks and live venues — never a resold paid API.
            </p>
            <div className="foot-tags">
              <a className="foot-tag" href="https://vulos.org" target="_blank" rel="noreferrer">
                <VulosMark size={17} /><span>part of <b>Vulos</b></span>
              </a>
              <a className="foot-tag" href={REPO} target="_blank" rel="noreferrer">
                <Icon.GitHub size={15} /><span>Source</span>
              </a>
            </div>
          </div>

          <Col head="Product" links={[
            ["Converter", "#convert"],
            ["Live board", "#board"],
            ["Policy rates", "#policy"],
            ["Accuracy", "#accuracy"],
            ["Docs", "#docs", onDocs],
          ]} />
          <Col head="Open sources" links={[
            ["ECB", "https://www.ecb.europa.eu"],
            ["SARB", "https://www.resbank.co.za"],
            ["BIS", "https://www.bis.org"],
            ["Coinbase", "https://www.coinbase.com"],
            ["Luno", "https://www.luno.com"],
          ]} />
          <Col head="Project" links={[
            ["GitHub", REPO],
            ["Where the data comes from", `${REPO}/blob/main/SOURCES.md`],
            ["Accuracy contract", `${REPO}/blob/main/ACCURACY.md`],
            ["Licence — MIT or Apache-2.0", `${REPO}/blob/main/LICENSE-MIT`],
            ["Third-party notices", "/licenses.txt"],
          ]} />
        </div>

        <div className="foot-bottom">
          <span className="foot-copy">
            © 2026 Vulos contributors · MIT OR Apache-2.0 · <a href={REPO} target="_blank" rel="noreferrer">source</a>
          </span>
          <span className="status">
            <span className="live-dot" />
            {live ? `${live} ${live === 1 ? "source" : "sources"} live` : "starting…"}
            {built ? ` · snapshot ${built.toLocaleTimeString()}` : ""}
          </span>
        </div>
      </div>
    </footer>
  );
}

function Col({ head, links }) {
  return (
    <div className="foot-col">
      <Label>{head}</Label>
      <ul>
        {links.map(([t, h, onClick]) => (
          <li key={t}>
            <a
              href={h} onClick={onClick}
              target={h.startsWith("http") ? "_blank" : undefined}
              rel={h.startsWith("http") ? "noreferrer" : undefined}
            >{t}</a>
          </li>
        ))}
      </ul>
    </div>
  );
}
