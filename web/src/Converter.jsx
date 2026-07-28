import React, { useEffect, useRef, useState } from "react";
import CurrencySelect from "./CurrencySelect.jsx";
import Works from "./Works.jsx";
import { Label, Seal, Icon } from "./ui.jsx";
import { convert, fmt, fmtAmount, ageLabel } from "./api.js";

const QUICK = [1, 10, 100, 1000, 10000];

/**
 * Converter — the instrument.
 *
 * Two legs stacked on an axle, the verdict seal on the output leg, and the
 * quality facets read out underneath. The rate is re-fetched on every input
 * change; while a request is in flight the previous result stays on screen at
 * reduced opacity rather than blanking, because a converter that flickers to
 * "—" on every keystroke feels broken even when it is working.
 */
export default function Converter({ currencies, defaultFrom = "USD", defaultTo = "ZAR" }) {
  const [from, setFrom] = useState(defaultFrom);
  const [to, setTo] = useState(defaultTo);
  const [amount, setAmount] = useState("100");
  const [out, setOut] = useState(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState(null);
  const [open, setOpen] = useState(false);
  const seq = useRef(0);

  useEffect(() => { setTo(defaultTo); }, [defaultTo]);

  useEffect(() => {
    if (!from || !to) return;
    const n = Number(amount);
    if (!Number.isFinite(n)) return;
    const id = ++seq.current;
    setBusy(true);
    convert(from, to, n)
      .then((r) => { if (id === seq.current) { setOut(r); setErr(null); } })
      .catch((e) => { if (id === seq.current) setErr(e.message); })
      .finally(() => { if (id === seq.current) setBusy(false); });
  }, [from, to, amount]);

  const rate = out?.rate;
  const q = rate?.quality;
  const r = rate?.rate;
  const swap = () => { setFrom(to); setTo(from); };

  const shown = out ? fmtAmount(out.result) : "—";
  const fit = shown.length > 12 ? "s2" : shown.length > 9 ? "s1" : "";

  return (
    <div className="instr">
      <div className="instr-head">
        <Label>Convert</Label>
        <span className="status">
          {rate ? <><span className="live-dot" />{ageLabel(rate.age_sec)} old · {rate.sources?.join(", ")}</> : "…"}
        </span>
      </div>

      <div className="instr-body">
        <div className="leg-field">
          <div className="leg-head"><Label>You send</Label></div>
          <div className="leg-row">
            <input
              className="amount num" type="number" inputMode="decimal" min="0"
              value={amount} aria-label="Amount to convert"
              onChange={(e) => setAmount(e.target.value)}
            />
            <CurrencySelect value={from} onChange={setFrom} options={currencies} label="from" />
          </div>
        </div>

        <div className="axle">
          <button className="swap" type="button" onClick={swap} title="Swap currencies" aria-label="Swap currencies">
            <Icon.Swap />
          </button>
        </div>

        <div className="leg-field">
          <div className="leg-head">
            <Label>They receive</Label>
            {q && <Seal q={q} size="md" />}
          </div>
          <div className="leg-row">
            <output className={`result num ${busy ? "stale" : ""} ${fit}`} title={shown}>
              {shown}
            </output>
            <CurrencySelect value={to} onChange={setTo} options={currencies} label="to" />
          </div>
        </div>

        <div className="chips">
          {QUICK.map((n) => (
            <button
              key={n} type="button"
              className={`chip ${Number(amount) === n ? "on" : ""}`}
              onClick={() => setAmount(String(n))}
            >
              {n.toLocaleString()}
            </button>
          ))}
        </div>

        {err && <div className="err" style={{ marginTop: 18, marginBottom: 0 }}><Icon.Warn />{err}</div>}

        {rate && q && (
          <>
            <div className="rule" style={{ margin: "24px 0 20px" }} />

            <div className="inverse">
              <Inverse a={from} b={to} v={fmt(r, 6)} />
              <Inverse a={to} b={from} v={fmt(1 / r, 6)} />
            </div>

            <div className="readout c6">
              <Facet l="grade" v={q.grade} grade={q.grade} />
              <Facet l="confidence" v={`${(q.confidence * 100).toFixed(0)}%`} />
              <Facet l="freshness" v={q.freshness} />
              <Facet l="directness" v={(q.directness || "").replace(/_/g, " ")} />
              <Facet l="source" v={(q.source_class || "").replace(/_/g, " ")} />
              <Facet
                l="corroboration"
                v={q.corroboration?.sources > 1 ? `${q.corroboration.sources} · ${fmt(q.corroboration.spread_bps, 1)}bps` : "single"}
              />
            </div>

            <button className={`disclose ${open ? "open" : ""}`} type="button" onClick={() => setOpen((o) => !o)}>
              <Icon.Caret />
              <Label>{open ? "Hide the working" : "Show the working"}</Label>
            </button>
            {open && <div className="panel-open"><Works rate={rate} from={from} to={to} /></div>}
          </>
        )}
      </div>
    </div>
  );
}

function Inverse({ a, b, v }) {
  return (
    <span className="num" style={{ fontSize: 13, color: "var(--text-3)" }}>
      1 {a} = <b style={{ color: "var(--text)", fontWeight: 600 }}>{v}</b> {b}
    </span>
  );
}

function Facet({ l, v, grade }) {
  return (
    <div className="ro">
      <Label>{l}</Label>
      <span className={`ro-v ${grade || ""}`} style={{ textTransform: grade ? "none" : "capitalize" }}>{v}</span>
    </div>
  );
}
