import React, { useEffect, useMemo, useRef, useState } from "react";
import { ccyName, ccyFlag } from "./currencies.js";

// CurrencySelect — a searchable currency dropdown (code + full name + flag),
// replacing the native <select>. Keyboard: type to filter, ↑/↓ to move, Enter
// to choose, Esc to close. Closes on outside click.
export default function CurrencySelect({ label, value, onChange, options }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const rootRef = useRef(null);
  const inputRef = useRef(null);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return options;
    return options.filter((c) => c.toLowerCase().includes(q) || ccyName(c).toLowerCase().includes(q));
  }, [query, options]);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e) => { if (rootRef.current && !rootRef.current.contains(e.target)) setOpen(false); };
    document.addEventListener("mousedown", onDoc);
    return () => document.removeEventListener("mousedown", onDoc);
  }, [open]);

  useEffect(() => { if (open) { setQuery(""); setActive(0); setTimeout(() => inputRef.current?.focus(), 10); } }, [open]);
  useEffect(() => { setActive(0); }, [query]);

  const choose = (c) => { onChange(c); setOpen(false); };

  const onKey = (e) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setActive((a) => Math.min(a + 1, filtered.length - 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)); }
    else if (e.key === "Enter") { e.preventDefault(); if (filtered[active]) choose(filtered[active]); }
    else if (e.key === "Escape") { setOpen(false); }
  };

  return (
    <div className="field csel" ref={rootRef}>
      <label>{label}</label>
      <button type="button" className={`csel-btn ${open ? "open" : ""}`} onClick={() => setOpen((o) => !o)} aria-haspopup="listbox" aria-expanded={open}>
        <span className="csel-flag">{ccyFlag(value)}</span>
        <span className="csel-code">{value}</span>
        <span className="csel-name">{ccyName(value)}</span>
        <svg className="csel-caret" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round"><path d="M6 9l6 6 6-6" /></svg>
      </button>
      {open && (
        <div className="csel-panel" role="listbox">
          <input
            ref={inputRef} className="csel-search" placeholder="Search currency…"
            value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={onKey}
          />
          <div className="csel-list">
            {filtered.length === 0 && <div className="csel-empty">no match</div>}
            {filtered.map((c, i) => (
              <button
                type="button" key={c}
                className={`csel-opt ${c === value ? "sel" : ""} ${i === active ? "active" : ""}`}
                onMouseEnter={() => setActive(i)} onClick={() => choose(c)} role="option" aria-selected={c === value}
              >
                <span className="csel-flag">{ccyFlag(c)}</span>
                <span className="csel-code">{c}</span>
                <span className="csel-name">{ccyName(c)}</span>
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
