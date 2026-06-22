// Thin client over the openrate JSON API.

export async function getMeta() {
  const r = await fetch("/api/v1/meta");
  if (!r.ok) throw new Error("meta failed");
  return r.json();
}

export async function getRates(base) {
  const r = await fetch(`/api/v1/rates?base=${encodeURIComponent(base)}`);
  if (!r.ok) throw new Error("rates failed");
  return r.json();
}

export async function convert(from, to, amount) {
  const q = new URLSearchParams({ from, to, amount: String(amount) });
  const r = await fetch(`/api/v1/convert?${q}`);
  if (!r.ok) throw new Error("convert failed");
  return r.json();
}

export function ageLabel(seconds) {
  if (seconds == null) return "—";
  const m = Math.round(seconds / 60);
  if (m < 60) return `${m}m old`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h}h old`;
  return `${Math.round(h / 24)}d old`;
}
