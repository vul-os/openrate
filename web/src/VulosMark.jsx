import React from "react";

// VulosMark — the canonical Vulos logo (the fluid "V" with teardrop, vendored
// from the OSS brand asset /brand/vulos.png). Rendered via CSS mask so the exact
// shape can be tinted to any theme colour or the openrate gradient.
// "Vulos" is isiZulu for "open".
export default function VulosMark({ size = 22, tint = "var(--text-2)", gradient = false, style, className }) {
  const bg = gradient ? "var(--grad)" : tint;
  return (
    <span
      role="img"
      aria-label="Vulos"
      className={className}
      style={{
        display: "inline-block",
        width: size,
        height: size,
        background: bg,
        WebkitMask: "url(/brand/vulos.png) center / contain no-repeat",
        mask: "url(/brand/vulos.png) center / contain no-repeat",
        flexShrink: 0,
        ...style,
      }}
    />
  );
}
