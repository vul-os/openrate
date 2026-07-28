// Display metadata for the economic areas the interest-rate engine carries.
//
// Flags are derived, not tabulated: an ISO-3166 alpha-2 code maps to a pair of
// regional-indicator code points, so every area the BIS ever adds renders
// without a code change. Only the non-country aggregates need naming.

const SPECIAL = {
  XM: { flag: "🇪🇺", name: "Euro area" },
};

export function areaFlag(code) {
  if (SPECIAL[code]) return SPECIAL[code].flag;
  if (!/^[A-Z]{2}$/.test(code || "")) return "◆";
  return String.fromCodePoint(...[...code].map((ch) => 0x1f1e6 + ch.charCodeAt(0) - 65));
}

// The engine names a series "South Africa — policy rate"; the card wants just
// the area, and the type is shown separately.
export function areaName(code, seriesName) {
  if (SPECIAL[code]) return SPECIAL[code].name;
  const head = (seriesName || "").split("—")[0].trim();
  return head || code;
}

// The areas featured with a full history card. ZA leads: openrate is anchored
// on the rand, so its own policy rate is the one a South African reader wants
// first, and the rest are the reserve currencies plus the large emerging blocs.
export const FEATURED = ["ZA", "US", "XM", "GB", "JP", "CN", "IN", "BR"];
