import React from "react";
import { Label, Icon } from "./ui.jsx";
import { ageLabel, fmt, fmtRate } from "./api.js";
import { ccyFlag } from "./currencies.js";

/**
 * Works — "show the work" for one rate.
 *
 * openrate's whole claim is that it does not hand you a number and ask you to
 * trust it: it hands you the walk through the currency graph that produced the
 * number, the individual source quotes behind each hop, and how far apart those
 * sources are. This component is that argument, drawn. It is shared by the
 * converter and by every expanded row on the board so the two can never drift.
 */
export default function Works({ rate, from, to }) {
  if (!rate) return null;
  const q = rate.quality || {};
  const c = q.corroboration || {};
  const legs = rate.legs || [];
  const quotes = (rate.quotes || []).slice().sort((a, b) => a.rate - b.rate);
  const path = rate.path?.length ? rate.path : [from, to];

  return (
    <div className="works">
      {/* ── the walk ───────────────────────────────────────────────── */}
      <div className="works-sec">
        <Label>Path</Label>
        <span className="tag">{rate.hops <= 1 ? "directly quoted" : `${rate.hops} hops`}</span>
      </div>
      <PathRail path={path} legs={legs} />
      {legs.length > 1 && (
        <p className="note" style={{ marginTop: 12 }}>
          {legs.map((l) => fmt(l.rate, 6)).join(" × ")} = <b style={{ color: "var(--teal)" }}>{fmt(rate.rate, 6)}</b>
          {" — "}the shortest walk in the graph, so a direct quote always wins over a triangulated cross.
        </p>
      )}

      {/* ── who is quoting it, and how far apart ───────────────────── */}
      {quotes.length > 0 && (
        <>
          <div className="works-sec">
            <Label>Sources quoting {from}→{to}</Label>
            <span className="tag">{quotes.length}</span>
          </div>
          <Dispersion quotes={quotes} mean={c.mean} spreadBps={c.spread_bps} />
        </>
      )}

      {/* ── the dispersion arithmetic ──────────────────────────────── */}
      {c.sources > 1 ? (
        <>
          <div className="works-sec"><Label>Agreement</Label></div>
          <div className="readout c4">
            <Cell l="mean" v={fmt(c.mean, 6)} />
            <Cell l="std dev" v={`${fmt(c.stdev_bps, 1)} bps`} />
            <Cell l="min – max" v={`${fmtRate(c.min)} – ${fmtRate(c.max)}`} />
            <Cell l="spread" v={`${fmt(c.spread_bps, 1)} bps`} warn={c.spread_bps > 50} />
          </div>
          {c.spread_bps > 50 && (
            <p className="note" style={{ marginTop: 11 }}>
              The venues disagree by more than half a percent. That is normal across a
              weekend or a market close — the central-bank leg is a daily fix, the
              exchange leg is live — but it is why this pair is not graded A.
            </p>
          )}
        </>
      ) : (
        <p className="note" style={{ marginTop: 14 }}>
          One source quotes this pair, so there is nothing to cross-check it against and
          no dispersion to report. Adding a second overlapping source is the single
          biggest thing you can do to raise this grade — see <code>.env.example</code>.
        </p>
      )}

      {q.caveats?.length > 0 && (
        <ul className="caveats">
          {q.caveats.map((t, i) => (
            <li key={i}><Icon.Warn style={{ flex: "none", marginTop: 2 }} /><span>{t}</span></li>
          ))}
        </ul>
      )}
    </div>
  );
}

/* ── the currency-graph walk ─────────────────────────────────────────── */
function PathRail({ path, legs }) {
  return (
    <div className="path">
      <div className="path-rail">
        {path.map((ccy, i) => (
          <React.Fragment key={`${ccy}-${i}`}>
            <div className={`path-node ${i === 0 || i === path.length - 1 ? "end" : ""}`}>
              <span className="dot">{ccy}</span>
              <span className="flag">{ccyFlag(ccy)}</span>
            </div>
            {i < path.length - 1 && (
              <div className="path-hop">
                <span className="hop-rate">{legs[i] ? fmt(legs[i].rate, 6) : "—"}</span>
                <span className="wire" />
                <span className="hop-meta">
                  {legs[i] ? `${legs[i].source} · ${ageLabel(legs[i].age_sec)}` : ""}
                </span>
              </div>
            )}
          </React.Fragment>
        ))}
      </div>
    </div>
  );
}

/* ── where the sources sit relative to one another ───────────────────── */
/* A scale, not a bar chart: the question is not "how big is each quote" (they
   are all nearly the same number) but "how far apart are they", so the axis is
   zoomed to the quotes themselves and the spread is the headline. */
function Dispersion({ quotes, mean, spreadBps }) {
  const vals = quotes.map((q) => q.rate);
  const lo = Math.min(...vals), hi = Math.max(...vals);
  const span = hi - lo;
  // With one distinct value there is no scale to draw; pad so the dots land
  // sensibly instead of dividing by zero.
  const pad = span > 0 ? span * 0.18 : Math.abs(hi || 1) * 0.0002;
  const min = lo - pad, max = hi + pad;
  const pct = (v) => ((v - min) / (max - min)) * 100;
  const wide = spreadBps > 50;

  return (
    <div className="disp">
      <div className="disp-scale">
        <span className="axis" />
        <span className="tick" style={{ left: 0 }} />
        <span className="tick" style={{ left: "calc(100% - 1px)" }} />
        {Number.isFinite(mean) && (
          <span className="mean" style={{ left: `${pct(mean)}%` }}><span>mean</span></span>
        )}
        {quotes.map((q) => (
          <span
            key={q.source}
            className={`disp-pt ${wide ? "wide" : ""}`}
            style={{ left: `${pct(q.rate)}%` }}
            title={`${q.source} — ${fmt(q.rate, 6)} · ${ageLabel(q.age_sec)} old`}
          >
            <span>{q.source}</span>
          </span>
        ))}
      </div>
      <div className="readout c4" style={{ marginTop: 26 }}>
        {quotes.map((q) => (
          <Cell key={q.source} l={q.source} v={fmt(q.rate, 6)} sub={ageLabel(q.age_sec)} />
        ))}
      </div>
    </div>
  );
}

function Cell({ l, v, sub, warn }) {
  return (
    <div className="ro">
      <Label>{l}</Label>
      <span className={`ro-v ${warn ? "warn" : ""}`} title={typeof v === "string" ? v : undefined}>{v}</span>
      {sub && <Label style={{ opacity: .7 }}>{sub} old</Label>}
    </div>
  );
}
