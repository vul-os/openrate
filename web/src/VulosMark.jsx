import React from "react";

// VulosMark — the canonical Vulos glyph (stylised "V" with teardrop counter),
// the same path used in the OSS favicon. "Vulos" is isiZulu for "open".
// Rendered with the openrate accent gradient by default so it reads on dark.
export default function VulosMark({ size = 22, gradient = true, fill, style, className }) {
  const id = "vulos-grad";
  return (
    <svg width={size} height={(size * 46) / 48} viewBox="0 0 48 46" fill="none"
      role="img" aria-label="Vulos" style={style} className={className}>
      {gradient && (
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="1" y2="1">
            <stop offset="0" stopColor="var(--good)" />
            <stop offset="1" stopColor="var(--brand)" />
          </linearGradient>
        </defs>
      )}
      <path
        d="M25.946 44.938c-.664.845-2.021.375-2.021-.698V33.937a2.26 2.26 0 0 0-2.262-2.262H10.287c-.92 0-1.456-1.04-.92-1.788l7.48-10.471c1.07-1.497 0-3.578-1.842-3.578H1.237c-.92 0-1.456-1.04-.92-1.788L10.013.474c.214-.297.556-.474.92-.474h28.894c.92 0 1.456 1.04.92 1.788l-7.48 10.471c-1.07 1.498 0 3.579 1.842 3.579h11.377c.943 0 1.473 1.088.89 1.83L25.947 44.94z"
        fill={fill || (gradient ? `url(#${id})` : "currentColor")}
      />
    </svg>
  );
}
