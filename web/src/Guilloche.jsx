import React, { useMemo } from "react";

/**
 * Guilloche — the engraved rosette behind the hero and the footer.
 *
 * This is a real hypotrochoid, the curve a geometric lathe traces and the same
 * construction used for the lathe-work on banknotes and share certificates:
 *
 *     x = (R − r)·cos t + d·cos(((R − r)/r)·t)
 *     y = (R − r)·sin t − d·sin(((R − r)/r)·t)
 *
 * Several rings are stacked at different (R, r, d) so the curves interfere the
 * way overlaid engraving plates do. It is drawn as plain SVG paths — no image,
 * no canvas, no library — so it stays crisp at any size and costs nothing to
 * ship. `decorative`, so it is hidden from assistive tech.
 */

// One closed hypotrochoid as an SVG path. `turns` is how many times t wraps
// 2π before the curve closes; for R/r ratios with a large denominator it needs
// to be r/gcd(R,r) to come back to the start.
function rosette(R, r, d, turns, steps) {
  const k = (R - r) / r;
  const pts = new Array(steps + 1);
  for (let i = 0; i <= steps; i++) {
    const t = (i / steps) * turns * Math.PI * 2;
    const x = (R - r) * Math.cos(t) + d * Math.cos(k * t);
    const y = (R - r) * Math.sin(t) - d * Math.sin(k * t);
    pts[i] = `${x.toFixed(2)},${y.toFixed(2)}`;
  }
  return `M${pts.join("L")}Z`;
}

const RINGS = [
  // R,   r,   d,  turns, steps, width, opacity
  [200, 31, 96, 31, 2200, 0.7, 0.55],
  [200, 47, 74, 47, 2600, 0.55, 0.4],
  [152, 23, 66, 23, 1700, 0.6, 0.45],
  [118, 17, 52, 17, 1300, 0.5, 0.32],
];

export default function Guilloche({ className = "", seed = 0 }) {
  const paths = useMemo(
    () => RINGS.map(([R, r, d, turns, steps, w, o]) => ({ d: rosette(R, r, d, turns, steps), w, o })),
    []
  );
  return (
    <div className={`guilloche ${className}`} aria-hidden="true">
      <svg viewBox="-220 -220 440 440" fill="none">
        <g className={`rose ${seed % 2 ? "rev" : ""}`} stroke="currentColor">
          {paths.map((p, i) => (
            <path key={i} d={p.d} strokeWidth={p.w} opacity={p.o} vectorEffect="non-scaling-stroke" />
          ))}
        </g>
        {/* a second plate, counter-rotating and scaled down, so the moiré between
            the two never repeats on a short cycle */}
        <g className={`rose ${seed % 2 ? "" : "rev"}`} stroke="currentColor" transform="scale(.62)">
          {paths.slice(0, 2).map((p, i) => (
            <path key={i} d={p.d} strokeWidth={p.w} opacity={p.o * 0.7} vectorEffect="non-scaling-stroke" />
          ))}
        </g>
      </svg>
    </div>
  );
}
