import React, { useEffect, useMemo, useState } from "react";
import { Label, Seal, Sparkline, Skeleton, Icon } from "./ui.jsx";
import { getInterestRates, getInterestSeries, dayLabel } from "./api.js";
import { areaFlag, areaName, FEATURED } from "./areas.js";

// Always two decimals: a grid of policy rates where one cell reads "7" and its
// neighbour "3.63" loses the alignment that makes the grid scannable.
const pct = (v) => Number(v).toFixed(2);

/**
 * Policy — central-bank policy rates.
 *
 * The binary has shipped an interest-rate engine (BIS + SARB, 48 areas, with
 * history) on /api/v1/interest/* for a while and nothing has ever rendered it.
 * It is graded on the same A–D scale as FX, which matters more here than it
 * does for FX: several BIS series are legacy pre-euro national rates last
 * observed in 1998, and the grade is the only thing standing between a reader
 * and quoting a twenty-eight-year-old number as today's policy rate.
 *
 * Featured areas get a full history card; everything else lists compactly.
 */
export default function Policy() {
  const [rates, setRates] = useState(null);
  const [series, setSeries] = useState({});
  const [err, setErr] = useState(null);
  const [showAll, setShowAll] = useState(false);

  useEffect(() => {
    let live = true;
    getInterestRates()
      .then((d) => { if (live) setRates(d.rates || []); })
      .catch((e) => { if (live) setErr(e.message); });
    return () => { live = false; };
  }, []);

  // Histories for the featured cards only — one request per card, in parallel,
  // rather than 48 requests for series nobody is looking at.
  useEffect(() => {
    if (!rates) return;
    let live = true;
    const wanted = rates.filter((r) => FEATURED.includes(r.area) && r.type === "policy");
    Promise.all(
      wanted.map((r) =>
        getInterestSeries(r.series)
          .then((d) => [r.series, d.history || []])
          .catch(() => [r.series, null])   // a missing history just hides the spark
      )
    ).then((pairs) => { if (live) setSeries(Object.fromEntries(pairs)); });
    return () => { live = false; };
  }, [rates]);

  const { featured, rest } = useMemo(() => {
    if (!rates) return { featured: [], rest: [] };
    const policy = rates.filter((r) => r.type === "policy");
    const rank = (r) => FEATURED.indexOf(r.area);
    return {
      featured: policy.filter((r) => FEATURED.includes(r.area)).sort((a, b) => rank(a) - rank(b)),
      rest: policy.filter((r) => !FEATURED.includes(r.area))
        .sort((a, b) => areaName(a.area, a.name).localeCompare(areaName(b.area, b.name))),
    };
  }, [rates]);

  if (err) return <div className="err"><Icon.Warn />Policy rates unavailable — {err}</div>;
  if (!rates) return <div className="board-shell"><Skeleton rows={6} /></div>;

  return (
    <>
      <div className="policy-grid">
        {featured.map((r) => <Card key={r.series} r={r} history={series[r.series]} />)}
      </div>

      <div className="rule" />

      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 16, flexWrap: "wrap" }}>
        <p className="note" style={{ maxWidth: "52ch" }}>
          {rest.length} more areas are carried. Several are legacy pre-euro national
          series the BIS still publishes — they are graded <b style={{ color: "var(--red)" }}>D</b> and
          dated so they can never be mistaken for a current rate.
        </p>
        <button className={`disclose ${showAll ? "open" : ""}`} type="button" onClick={() => setShowAll((v) => !v)}>
          <Icon.Caret />
          <Label>{showAll ? "Hide" : `Show all ${rest.length}`}</Label>
        </button>
      </div>

      {showAll && (
        <div className="board-shell panel-open" style={{ marginTop: 18 }}>
          <div className="board-scroll">
            <table className="board">
              <thead>
                <tr>
                  <th>Area</th><th className="r">Rate</th><th>Grade</th>
                  <th className="col-age">Observed</th><th className="col-src">Source</th>
                </tr>
              </thead>
              <tbody>
                {rest.map((r) => (
                  <tr key={r.series}>
                    <td>
                      <span className="b-ccy">
                        <span className="flag">{areaFlag(r.area)}</span>
                        <span className="code">{r.area}</span>
                        <span className="name col-name">{areaName(r.area, r.name)}</span>
                      </span>
                    </td>
                    <td className="r"><span className="b-rate">{pct(r.value)}%</span></td>
                    <td><Seal q={r.quality} /></td>
                    <td className="b-mut col-age">{dayLabel(r.date)}</td>
                    <td className="b-mut col-src">{r.source}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </>
  );
}

function Card({ r, history }) {
  // Move over the trailing year of the series, so "unchanged since the last cut"
  // reads as flat rather than as no data.
  const delta = useMemo(() => {
    if (!history || history.length < 2) return null;
    const last = history[history.length - 1];
    const cutoff = new Date(last.date);
    cutoff.setFullYear(cutoff.getFullYear() - 1);
    const prior = history.find((p) => new Date(p.date) >= cutoff) || history[0];
    return last.value - prior.value;
  }, [history]);

  const dir = delta == null ? "flat" : delta > 0.001 ? "up" : delta < -0.001 ? "down" : "flat";
  const arrow = dir === "up" ? "▲" : dir === "down" ? "▼" : "—";

  return (
    <article className="policy">
      <div className="policy-head">
        <span className="policy-area">
          <span className="flag">{areaFlag(r.area)}</span>
          <span className="nm">{areaName(r.area, r.name)}</span>
        </span>
        <Seal q={r.quality} />
      </div>

      <div className="policy-val">
        <b className="num">{pct(r.value)}</b><i>%</i>
      </div>

      <Sparkline points={history} id={r.series.replace(/\W/g, "")} />

      <div className="policy-foot">
        <Label>{dayLabel(r.date)}</Label>
        <span className={`delta ${dir}`} title={delta == null ? "no history" : "change over the trailing year"}>
          {arrow}{delta != null && dir !== "flat" ? ` ${Math.abs(delta).toFixed(2)} pp` : ""}
        </span>
      </div>
    </article>
  );
}
