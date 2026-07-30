import React, { useEffect, useMemo, useRef, useState } from "react";

/* ── Reveal — scroll-in, once, honours prefers-reduced-motion ─────────── */
export function Reveal({ children, delay = 0, as: Tag = "div", className = "", ...rest }) {
  const ref = useRef(null);
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) { setShown(true); return; }
    const io = new IntersectionObserver(
      ([e]) => { if (e.isIntersecting) { setShown(true); io.disconnect(); } },
      { threshold: 0.08, rootMargin: "0px 0px -6% 0px" }
    );
    io.observe(el);
    return () => io.disconnect();
  }, []);
  return (
    <Tag ref={ref} className={`reveal ${shown ? "in" : ""} ${className}`} style={{ transitionDelay: `${delay}ms` }} {...rest}>
      {children}
    </Tag>
  );
}

/* ── Label — condensed board lettering ───────────────────────────────── */
export const Label = ({ children, className = "", ...r }) => (
  <span className={`lbl ${className}`} {...r}>{children}</span>
);

/* ── Eyebrow — a label struck with an engraver's diamond and a rule ──── */
export const Eyebrow = ({ children }) => (
  <span className="eyebrow"><Label>{children}</Label></span>
);

/* ── useScrollSpy ────────────────────────────────────────────────────── */
export function useScrollSpy(ids) {
  const [active, setActive] = useState(ids[0]);
  const key = ids.join(",");
  useEffect(() => {
    const obs = new IntersectionObserver(
      (entries) => entries.forEach((e) => { if (e.isIntersecting) setActive(e.target.id); }),
      { rootMargin: "-42% 0px -52% 0px", threshold: 0 }
    );
    key.split(",").forEach((id) => { const el = document.getElementById(id); if (el) obs.observe(el); });
    return () => obs.disconnect();
  }, [key]);
  return active;
}

/* ── ThemeToggle — ink ⇄ banknote paper ──────────────────────────────── */
export function ThemeToggle() {
  const [theme, setTheme] = useState(() => {
    try { return localStorage.getItem("or-theme") || "dark"; } catch { return "dark"; }
  });
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.querySelector('meta[name="theme-color"]')?.setAttribute("content", theme === "dark" ? "#06090E" : "#F2EFE6");
    try { localStorage.setItem("or-theme", theme); } catch { /* private mode — theme just won't persist */ }
  }, [theme]);
  const dark = theme === "dark";
  return (
    <button
      className="icon-btn" type="button"
      aria-label={dark ? "Switch to paper theme" : "Switch to ink theme"}
      title={dark ? "Paper" : "Ink"}
      onClick={() => setTheme(dark ? "light" : "dark")}
    >
      {dark ? <Icon.Sun /> : <Icon.Moon />}
    </button>
  );
}

/* ── Seal — a grade, struck like a stamp ─────────────────────────────── */
export function Seal({ q, size = "sm", title }) {
  if (!q?.grade) return null;
  const c = q.corroboration || {};
  const tip = title ?? [
    `grade ${q.grade} · confidence ${(q.confidence * 100).toFixed(0)}%`,
    q.freshness,
    q.directness,
    q.source_class && `${q.source_class} source`.replace(/_/g, " "),
    c.sources > 1 ? `${c.sources} sources, ${c.spread_bps} bps apart` : "single source",
  ].filter(Boolean).join(" · ");
  return <span className={`seal ${size} ${q.grade}`} title={tip} aria-label={`grade ${q.grade}`}>{q.grade}</span>;
}

/* ── Sparkline — a policy-rate history, drawn as a step line ─────────── */
/* Policy rates hold flat then jump at a meeting, so this is a stepped path,
   not a smoothed one: interpolating between decisions would draw moves that
   never happened. */
export function Sparkline({ points, w = 240, h = 34, id }) {
  const d = useMemo(() => {
    if (!points || points.length < 2) return null;
    const vals = points.map((p) => p.value);
    const lo = Math.min(...vals), hi = Math.max(...vals);
    const span = hi - lo || 1;
    const pad = 3;
    const x = (i) => (i / (points.length - 1)) * w;
    const y = (v) => h - pad - ((v - lo) / span) * (h - pad * 2);
    let line = `M0,${y(vals[0]).toFixed(2)}`;
    for (let i = 1; i < points.length; i++) line += `H${x(i).toFixed(2)}V${y(vals[i]).toFixed(2)}`;
    return { line, area: `${line}V${h}H0Z`, tip: [w, y(vals[vals.length - 1])] };
  }, [points, w, h]);

  if (!d) return <div className="spark" aria-hidden="true" />;
  const gid = `sf-${id}`;
  return (
    <svg className="spark" viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" aria-hidden="true">
      <defs>
        <linearGradient id={gid} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor="var(--teal)" stopOpacity=".24" />
          <stop offset="100%" stopColor="var(--teal)" stopOpacity="0" />
        </linearGradient>
      </defs>
      <path className="area" d={d.area} fill={`url(#${gid})`} />
      <path className="line" d={d.line} />
      <circle className="tip" cx={d.tip[0]} cy={d.tip[1]} r="2.4" />
    </svg>
  );
}

/* ── Skeleton rows for the board while the first snapshot lands ──────── */
export const Skeleton = ({ rows = 8 }) => (
  <div style={{ padding: "16px", display: "flex", flexDirection: "column", gap: 14 }}>
    {Array.from({ length: rows }, (_, i) => (
      <div key={i} className="skel" style={{ height: 15, width: `${92 - (i % 4) * 11}%` }} />
    ))}
  </div>
);

/* ── icons ───────────────────────────────────────────────────────────── */
const s = { fill: "none", stroke: "currentColor", strokeWidth: 1.8, strokeLinecap: "round", strokeLinejoin: "round" };

export const Icon = {
  Sun: (p) => <svg width="16" height="16" viewBox="0 0 24 24" {...s} {...p}><circle cx="12" cy="12" r="4.2" /><path d="M12 2.4v2M12 19.6v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M2.4 12h2M19.6 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4" /></svg>,
  Moon: (p) => <svg width="16" height="16" viewBox="0 0 24 24" {...s} {...p}><path d="M20.8 13.2A8.6 8.6 0 1 1 10.8 3.2a6.7 6.7 0 0 0 10 10z" /></svg>,
  Swap: (p) => <svg width="16" height="16" viewBox="0 0 24 24" {...s} {...p}><path d="M4 8h13M13 4l4 4-4 4M20 16H7M11 12l-4 4 4 4" /></svg>,
  Chevron: (p) => <svg width="12" height="12" viewBox="0 0 24 24" {...s} strokeWidth="2.4" {...p}><path d="M6 9l6 6 6-6" /></svg>,
  Caret: (p) => <svg width="11" height="11" viewBox="0 0 24 24" {...s} strokeWidth="2.6" {...p}><path d="M9 6l6 6-6 6" /></svg>,
  Arrow: (p) => <svg width="14" height="14" viewBox="0 0 24 24" {...s} strokeWidth="2" {...p}><path d="M5 12h14M13 6l6 6-6 6" /></svg>,
  Warn: (p) => <svg width="14" height="14" viewBox="0 0 24 24" {...s} {...p}><path d="M12 3.5 22 20H2L12 3.5zM12 10v4.4M12 17.2v.01" /></svg>,
  Menu: (p) => <svg width="17" height="17" viewBox="0 0 24 24" {...s} strokeWidth="2" {...p}><path d="M4 7h16M4 12h16M4 17h16" /></svg>,
  Close: (p) => <svg width="17" height="17" viewBox="0 0 24 24" {...s} strokeWidth="2" {...p}><path d="M6 6l12 12M18 6L6 18" /></svg>,
  GitHub: ({ size = 17, ...p }) => (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" {...p}>
      <path d="M12 .5C5.73.5.5 5.74.5 12.02c0 5.1 3.29 9.41 7.86 10.94.58.1.79-.25.79-.56v-2.02c-3.2.7-3.88-1.37-3.88-1.37-.53-1.35-1.29-1.71-1.29-1.71-1.05-.72.08-.71.08-.71 1.16.08 1.77 1.2 1.77 1.2 1.03 1.77 2.7 1.26 3.36.96.1-.75.4-1.26.73-1.55-2.56-.29-5.26-1.28-5.26-5.71 0-1.26.45-2.3 1.19-3.11-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.18 1.19a11 11 0 0 1 5.8 0c2.2-1.5 3.17-1.19 3.17-1.19.63 1.59.23 2.76.12 3.05.74.81 1.18 1.85 1.18 3.11 0 4.44-2.7 5.42-5.28 5.7.41.36.78 1.07.78 2.16v3.2c0 .31.21.67.8.56A11.53 11.53 0 0 0 23.5 12.02C23.5 5.74 18.27.5 12 .5z" />
    </svg>
  ),
};

/* ── the openrate mark: two arcs, a ratio made a shape ───────────────── */
export const Mark = ({ size = 26, className = "" }) => (
  <svg className={`mark ${className}`} width={size} height={size} viewBox="0 0 120 120" role="img" aria-label="openrate">
    <path d="M26 58 A42 42 0 0 1 110 58 Z" fill="var(--blue)" />
    <path d="M10 62 A42 42 0 0 0 94 62 Z" fill="var(--teal)" />
  </svg>
);
