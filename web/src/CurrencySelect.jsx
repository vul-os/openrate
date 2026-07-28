import React, { useEffect, useMemo, useRef, useState } from "react";
import { ccyName, ccyFlag } from "./currencies.js";
import { Icon } from "./ui.jsx";

/**
 * CurrencySelect — a searchable currency picker (code + name + flag).
 *
 * A native <select> holding 43 three-letter codes is unusable; this filters on
 * both the code and the full name, so "rand" and "ZAR" both find ZAR.
 * Keyboard: type to filter, ↑/↓ to move, Enter to choose, Esc to close.
 */
export default function CurrencySelect({ value, onChange, options = [], compact = false, label }) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const rootRef = useRef(null);
  const inputRef = useRef(null);
  const listRef = useRef(null);

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

  useEffect(() => {
    if (!open) return;
    setQuery("");
    setActive(Math.max(0, options.indexOf(value)));
    const t = setTimeout(() => inputRef.current?.focus(), 10);
    return () => clearTimeout(t);
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => { setActive(0); }, [query]);

  // Keep the highlighted option in view when arrowing past the fold.
  useEffect(() => {
    if (!open) return;
    listRef.current?.children[active]?.scrollIntoView({ block: "nearest" });
  }, [active, open]);

  const choose = (c) => { onChange(c); setOpen(false); };

  const onKey = (e) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setActive((a) => Math.min(a + 1, filtered.length - 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)); }
    else if (e.key === "Enter") { e.preventDefault(); if (filtered[active]) choose(filtered[active]); }
    else if (e.key === "Escape") { e.preventDefault(); setOpen(false); }
  };

  return (
    <div className="csel" ref={rootRef}>
      <button
        type="button"
        className={`csel-btn ${open ? "open" : ""} ${compact ? "compact" : ""}`}
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="listbox" aria-expanded={open}
        aria-label={`${label ? `${label} — ` : ""}${value}, ${ccyName(value)}`}
      >
        <span className="csel-flag">{ccyFlag(value)}</span>
        <span className="csel-code">{value}</span>
        <span className="csel-name">{ccyName(value)}</span>
        <Icon.Chevron className="csel-caret" />
      </button>

      {open && (
        <div className="csel-panel">
          <input
            ref={inputRef} className="csel-search" placeholder="Search currency…"
            value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={onKey}
            aria-label="Search currency"
          />
          <div className="csel-list" ref={listRef} role="listbox">
            {filtered.length === 0 && <div className="csel-empty">No currency matches “{query}”.</div>}
            {filtered.map((c, i) => (
              <button
                type="button" key={c} role="option" aria-selected={c === value}
                className={`csel-opt ${c === value ? "sel" : ""} ${i === active ? "active" : ""}`}
                onMouseEnter={() => setActive(i)} onClick={() => choose(c)}
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
