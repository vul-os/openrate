import React, { useEffect, useRef, useState } from "react";

// Reveal — gentle scroll-in (fade + rise), respects prefers-reduced-motion.
export function Reveal({ children, delay = 0, as: Tag = "div", className = "", ...rest }) {
  const ref = useRef(null);
  const [shown, setShown] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) { setShown(true); return; }
    const io = new IntersectionObserver(
      ([e]) => { if (e.isIntersecting) { setShown(true); io.disconnect(); } },
      { threshold: 0.12, rootMargin: "0px 0px -8% 0px" }
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

// Eyebrow — small-caps mono label with a leading hairline rule.
export function Eyebrow({ children }) {
  return <span className="eyebrow">{children}</span>;
}

// ThemeToggle — dark/light, persisted to localStorage, sticks to Vulos tokens.
export function ThemeToggle() {
  const [theme, setTheme] = useState(() => localStorage.getItem("or-theme") || "dark");
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem("or-theme", theme);
  }, [theme]);
  const dark = theme === "dark";
  return (
    <button className="theme-toggle" title={dark ? "Switch to light" : "Switch to dark"} aria-label="Toggle theme"
      onClick={() => setTheme(dark ? "light" : "dark")}>
      {dark ? (
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="12" cy="12" r="4" /><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" /></svg>
      ) : (
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z" /></svg>
      )}
    </button>
  );
}

// Arrow — inline link with a nudging chevron.
export function Arrow({ href, children, onClick }) {
  return (
    <a className="arrow-link" href={href} onClick={onClick}>
      {children}
      <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14M13 6l6 6-6 6" /></svg>
    </a>
  );
}
