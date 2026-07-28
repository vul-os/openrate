import React, { useMemo, useState } from "react";
import Works from "./Works.jsx";
import { Label, Seal, Skeleton } from "./ui.jsx";
import { ageLabel, fmtRate } from "./api.js";
import { ccyFlag, ccyName } from "./currencies.js";

const GRADE_RANK = { A: 0, B: 1, C: 2, D: 3 };

const COLS = [
  { k: "ccy",   t: "Currency", cls: "",          col: "c-ccy" },
  { k: "rate",  t: "Rate",     cls: "r",         col: "c-rate" },
  { k: "grade", t: "Grade",    cls: "",          col: "c-grade" },
  { k: "hops",  t: "Path",     cls: "",          col: "c-hops" },
  { k: "age",   t: "Age",      cls: "r col-age", col: "c-age" },
  { k: "src",   t: "Source",   cls: "col-src",   col: "c-src" },
];

/**
 * Board — every pair openrate can reach from the current anchor.
 *
 * Sortable and filterable, because the interesting question is rarely "what is
 * 1 ZAR in AED" — it is "which of these numbers should I not trust", and that
 * means sorting by grade or by age. Each row opens into the same Works panel
 * the converter uses.
 */
export default function Board({ rates, base, meta, builtAt }) {
  const [openCcy, setOpen] = useState(null);
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState({ k: "ccy", dir: 1 });

  const rows = useMemo(() => {
    if (!rates) return [];
    const q = query.trim().toLowerCase();
    const list = Object.entries(rates).filter(
      ([ccy]) => !q || ccy.toLowerCase().includes(q) || ccyName(ccy).toLowerCase().includes(q)
    );
    const val = ([ccy, p]) => {
      switch (sort.k) {
        case "rate":  return p.rate;
        case "grade": return GRADE_RANK[p.quality?.grade] ?? 9;
        case "hops":  return p.hops ?? 9;
        case "age":   return p.age_sec ?? Infinity;
        case "src":   return p.quality?.source_class || "";
        default:      return ccy;
      }
    };
    return list.sort((a, b) => {
      const x = val(a), y = val(b);
      const c = typeof x === "string" ? x.localeCompare(y) : x - y;
      // Ties fall back to the code so the order is stable across re-sorts.
      return (c || a[0].localeCompare(b[0])) * sort.dir;
    });
  }, [rates, query, sort]);

  const click = (k) => setSort((s) => (s.k === k ? { k, dir: -s.dir } : { k, dir: 1 }));
  const sources = meta?.sources ?? [];

  return (
    <div className="board-shell">
      <div className="board-bar">
        <Label>1 {base} buys</Label>
        <input
          className="board-search" type="search" placeholder="Filter currencies…"
          value={query} onChange={(e) => setQuery(e.target.value)} aria-label="Filter currencies"
        />
        <Label>{rows.length} {rows.length === 1 ? "pair" : "pairs"}</Label>
      </div>

      <div className="board-scroll">
        {!rates ? (
          <Skeleton rows={9} />
        ) : (
          <table className="board">
            <colgroup>{COLS.map((c) => <col key={c.k} className={c.col} />)}</colgroup>
            <thead>
              <tr>
                {COLS.map((c) => (
                  <th
                    key={c.k} className={`${c.cls} ${sort.k === c.k ? "sorted" : ""}`}
                    onClick={() => click(c.k)}
                    aria-sort={sort.k === c.k ? (sort.dir === 1 ? "ascending" : "descending") : "none"}
                  >
                    {c.t}
                    {sort.k === c.k && <span className="caret">{sort.dir === 1 ? "▲" : "▼"}</span>}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 && (
                <tr><td colSpan={COLS.length} style={{ padding: 28, textAlign: "center", color: "var(--text-4)" }}>
                  No currency matches “{query}”.
                </td></tr>
              )}
              {rows.map(([ccy, p]) => {
                const isOpen = openCcy === ccy;
                return (
                  <React.Fragment key={ccy}>
                    <tr className={`row ${isOpen ? "open" : ""}`} onClick={() => setOpen(isOpen ? null : ccy)}>
                      <td>
                        <span className="b-ccy">
                          <span className="flag">{ccyFlag(ccy)}</span>
                          <span className="code">{ccy}</span>
                          <span className="name col-name">{ccyName(ccy)}</span>
                        </span>
                      </td>
                      <td className="r"><span className="b-rate">{fmtRate(p.rate)}</span></td>
                      <td><Seal q={p.quality} /></td>
                      <td><Hops n={p.hops} /></td>
                      <td className="r b-mut col-age">{ageLabel(p.age_sec)}</td>
                      <td className="b-mut col-src">{(p.quality?.source_class || "").replace(/_/g, " ")}</td>
                    </tr>
                    {isOpen && (
                      <tr>
                        <td className="detail-cell" colSpan={COLS.length}>
                          <div className="detail-inner panel-open">
                            <Works rate={p} from={base} to={ccy} />
                          </div>
                        </td>
                      </tr>
                    )}
                  </React.Fragment>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      <div className="board-foot">
        {sources.map((s) => (
          <span className="src-pill" key={s.name} title={s.last_error || `${s.edges} edges`}>
            <i className={s.last_error || !s.edges ? "dead" : ""} />
            <b>{s.name}</b> {s.edges}
          </span>
        ))}
        {builtAt && <span style={{ marginLeft: "auto" }}>snapshot {new Date(builtAt).toLocaleTimeString()}</span>}
      </div>
    </div>
  );
}

/* Hop pips — how many graph edges this rate crossed. One filled pip is a
   direct quote; more pips means the number is a product of other numbers. */
function Hops({ n = 1 }) {
  const total = 3;
  return (
    <span className="hops" title={n <= 1 ? "directly quoted" : `${n} hops through the graph`}>
      {Array.from({ length: total }, (_, i) => <i key={i} className={i < n ? "" : "off"} />)}
    </span>
  );
}
