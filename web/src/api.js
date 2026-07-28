// Thin client over the openrate JSON API.

async function get(path) {
  const r = await fetch(path);
  if (!r.ok) throw new Error(`${path.replace(/\?.*/, "")} → ${r.status}`);
  return r.json();
}

export const getMeta = () => get("/api/v1/meta");
export const getRates = (base) => get(`/api/v1/rates?base=${encodeURIComponent(base)}`);
export const convert = (from, to, amount) =>
  get(`/api/v1/convert?${new URLSearchParams({ from, to, amount: String(amount) })}`);

// Policy / interest rates — a separate engine on the same binary (flat series,
// not a currency graph), served under /api/v1/interest/*.
export const getInterestRates = () => get("/api/v1/interest/rates");
export const getInterestSeries = (id) => get(`/api/v1/interest/series?id=${encodeURIComponent(id)}`);

export function ageLabel(seconds) {
  if (seconds == null) return "—";
  if (seconds < 90) return `${Math.max(1, Math.round(seconds))}s`;
  const m = Math.round(seconds / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h}h`;
  const d = Math.round(h / 24);
  if (d < 400) return `${d}d`;
  return `${(d / 365).toFixed(1)}y`;
}

// Human date for a policy-rate observation ("14 Jul 2026").
export function dayLabel(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  if (Number.isNaN(+d)) return "—";
  return d.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}

export const fmt = (n, d = 4) =>
  Number(n).toLocaleString(undefined, { maximumFractionDigits: d });

// Converted amounts: money precision for anything at or above a unit, more
// decimals below it so a fraction of a bitcoin is still a number.
export function fmtAmount(n) {
  const v = Number(n);
  if (!Number.isFinite(v)) return "—";
  const abs = Math.abs(v);
  const d = abs === 0 || abs >= 1 ? 2 : abs >= 0.01 ? 4 : 8;
  return v.toLocaleString(undefined, { minimumFractionDigits: d, maximumFractionDigits: d });
}

// Significant-figure formatting for board rates: keeps 1 USD = 0.0000112 BTC
// and 1 ZAR = 8.7 JPY both legible without a per-currency table.
export function fmtRate(n) {
  const v = Number(n);
  if (!Number.isFinite(v) || v === 0) return "0";
  const abs = Math.abs(v);
  const d = abs >= 1000 ? 2 : abs >= 1 ? 4 : abs >= 0.01 ? 6 : 8;
  return v.toLocaleString(undefined, { minimumFractionDigits: d, maximumFractionDigits: d });
}
