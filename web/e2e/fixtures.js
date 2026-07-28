/**
 * e2e/fixtures.js — shared Playwright helpers for the openrate web E2E layer.
 *
 * Two things live here:
 *
 *   1. `installApi(page)` — an in-browser mock of the openrate JSON API
 *      (/api/v1/meta, /api/v1/rates, /api/v1/convert, and the /api/v1/interest/*
 *      policy-rate endpoints) via page.route, so the suite runs with no Go
 *      binary and no live rate feeds. The shapes mirror what the engine actually
 *      serves (see internal/api and internal/ratesapi) — a rate carries a
 *      `quality` block (grade/confidence/freshness/directness/source_class/
 *      corroboration) plus `legs` and `quotes`, which is what App.jsx renders.
 *
 *      The interest routes must be mocked even by specs that never look at the
 *      policy section: Policy.jsx fetches on mount, and an unmocked route would
 *      leave a failed request behind for watchForCrashes to flag.
 *
 *   2. `watchForCrashes(page)` — the crash recorder used by EVERY spec. It
 *      records uncaught exceptions ("pageerror") and failed asset requests.
 *      A React app that throws while rendering unmounts to an EMPTY root and
 *      still returns HTTP 200 — the exact blank-screen failure that shipped —
 *      so "no pageerror" is asserted as a hard gate, not a warning.
 */

import { test as base, expect } from "@playwright/test";

const RATE = (rate, grade = "A", extra = {}) => ({
  rate,
  age_sec: 42,
  hops: 1,
  quality: {
    grade,
    confidence: 0.94,
    freshness: "fresh",
    directness: "direct",
    source_class: "central_bank",
    corroboration: { sources: 2, spread_bps: 3, mean: rate, stdev_bps: 2, min: rate * 0.999, max: rate * 1.001 },
    caveats: [],
  },
  legs: [{ from: "ZAR", to: "USD", rate, source: "sarb", age_sec: 42 }],
  quotes: [
    { source: "sarb", rate: rate * 0.999, age_sec: 40 },
    { source: "ecb", rate: rate * 1.001, age_sec: 55 },
  ],
  ...extra,
});

export const META = {
  default_base: "ZAR",
  currencies: ["ZAR", "USD", "EUR", "GBP", "JPY"],
  sources: [
    { name: "sarb", edges: 12, last_error: "" },
    { name: "ecb", edges: 31, last_error: "" },
  ],
};

// Policy rates. ZA is featured (so it renders a card and pulls a history) and
// AT is deliberately a stale legacy series graded D — the case the section
// exists to make un-mistakable, and the one the "show all" list has to carry.
export const INTEREST = {
  count: 2,
  built_at: "2026-07-14T09:00:00Z",
  rates: [
    {
      series: "za.policy", area: "ZA", type: "policy", name: "South Africa — policy rate",
      value: 6.75, date: "2026-07-10T00:00:00Z", age_sec: 3600, source: "sarbrates",
      quality: { grade: "A", confidence: 0.95, freshness: "current", source_class: "central_bank",
                 corroboration: { sources: 2, spread_bps: 0, agree: true }, caveats: [] },
    },
    {
      series: "at.policy", area: "AT", type: "policy", name: "Austria — policy rate",
      value: 3, date: "1998-12-31T00:00:00Z", age_sec: 870201884, source: "bis",
      quality: { grade: "D", confidence: 0.35, freshness: "old", source_class: "official_aggregator",
                 corroboration: { sources: 1, spread_bps: 0, agree: false },
                 caveats: ["single source — not independently corroborated"] },
    },
  ],
};

export const SERIES = {
  built_at: "2026-07-14T09:00:00Z",
  history: [
    { date: "2025-08-01T00:00:00Z", value: 7.25, source: "sarbrates" },
    { date: "2025-11-01T00:00:00Z", value: 7.0, source: "sarbrates" },
    { date: "2026-03-01T00:00:00Z", value: 6.75, source: "sarbrates" },
    { date: "2026-07-10T00:00:00Z", value: 6.75, source: "sarbrates" },
  ],
};

// 1 ZAR = these. Deliberately includes a B-grade row so the grade badge
// rendering is exercised for more than the happy "A" path.
export const RATES = {
  base: "ZAR",
  built_at: "2026-07-14T09:00:00Z",
  rates: {
    USD: RATE(0.055),
    EUR: RATE(0.05, "B"),
    GBP: RATE(0.043),
  },
};

/** Attach the mocked openrate API to a page. Call BEFORE page.goto(). */
export async function installApi(page) {
  const json = (route, body) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });

  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;

    // Interest routes first: "/api/v1/interest/rates" also ends with "/rates",
    // so matching the FX routes ahead of these would swallow them and serve a
    // currency snapshot to the policy section.
    if (path.endsWith("/interest/rates")) return json(route, INTEREST);
    if (path.endsWith("/interest/series")) return json(route, SERIES);
    if (path.endsWith("/interest/meta")) {
      return json(route, { area_count: 2, areas: ["ZA", "AT"], built_at: INTEREST.built_at, series: [] });
    }

    if (path.endsWith("/meta")) return json(route, META);

    if (path.endsWith("/rates")) {
      const b = url.searchParams.get("base") || "ZAR";
      return json(route, { ...RATES, base: b });
    }

    if (path.endsWith("/convert")) {
      const from = url.searchParams.get("from");
      const to = url.searchParams.get("to");
      const amount = Number(url.searchParams.get("amount") || 0);
      // A deterministic, non-1 rate so the converter's output is a pure
      // function of (from, to, amount) — the assertions below can compute the
      // expected number, which is what makes "the interaction actually worked"
      // provable rather than "some digits appeared".
      const rate = from === to ? 1 : 18.5;
      return json(route, { from, to, amount, result: amount * rate, rate: RATE(rate) });
    }

    return route.fulfill({ status: 404, body: "not found" });
  });
}

/**
 * Record uncaught exceptions and dead asset requests for the life of the page.
 * Returns { pageErrors, failedRequests } — assert them EMPTY in every spec.
 */
export function watchForCrashes(page) {
  const pageErrors = [];
  const failedRequests = [];
  page.on("pageerror", (err) => pageErrors.push(`${err.name}: ${err.message}`));
  page.on("requestfailed", (req) => {
    // Only care about the app's own assets — an aborted mock route or an
    // outbound analytics ping is not a boot failure.
    if (new URL(req.url()).origin === new URL(page.url() || "http://localhost").origin) {
      failedRequests.push(`${req.url()} — ${req.failure()?.errorText}`);
    }
  });
  return { pageErrors, failedRequests };
}

/** `openrate` fixture: a page with the API mocked and crashes recorded. */
export const test = base.extend({
  openrate: async ({ page }, use) => {
    await installApi(page);
    const crashes = watchForCrashes(page);
    await use({ page, ...crashes });
    // Every test using this fixture is a boot guard too: no test may leave an
    // uncaught exception behind.
    expect(crashes.pageErrors, "uncaught exception(s) in the built bundle").toEqual([]);
  },
});

export { expect };
