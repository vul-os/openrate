/**
 * policy.e2e.js — the policy-rate section.
 *
 * /api/v1/interest/* has shipped in the binary for a while with nothing
 * rendering it. Now that it has a surface, it needs the same floor as the rest:
 *
 *   - Policy.jsx makes TWO round trips (the rate list, then one history request
 *     per featured area). A regression that leaves the second unfired shows
 *     empty cards, which looks like "no data" rather than a bug.
 *   - The stale-series case is the reason this section is graded at all. The
 *     BIS publishes legacy pre-euro national series last observed in the 1990s;
 *     if the D grade or the observation date stopped rendering, a 1998 number
 *     would read as today's policy rate. That is the worst failure this app can
 *     have, so it is asserted explicitly.
 *   - The section must not take the page down when the interest engine is off:
 *     `-interest-sources ""` is a supported configuration and those routes then
 *     404, which must degrade to a message rather than an uncaught throw.
 */

import { test, expect } from "./fixtures.js";
import { test as bare } from "@playwright/test";
import { installApi, watchForCrashes, settled, META, RATES } from "./fixtures.js";

test("renders a featured policy card with its history", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");
  await settled(page);

  const card = page.locator(".policy").first();
  await expect(card).toBeVisible();
  await expect(card).toContainText("South Africa");

  // Always two decimals, so a grid of rates stays aligned — 6.75, not 6.75 next
  // to a bare "3".
  await expect(card.locator(".policy-val b")).toHaveText("6.75");
  await expect(card.locator(".seal")).toHaveText("A");

  // The sparkline only exists once the per-series history request resolved, so
  // this is the assertion that the second round trip actually happened.
  await expect(card.locator("svg.spark path.line")).toBeVisible();

  // 6.75 today against 7.25 a year back — a half-point of cuts, shown as a fall.
  await expect(card.locator(".delta")).toHaveClass(/down/);
  await expect(card.locator(".delta")).toContainText("0.50");
});

test("a stale legacy series is graded D and dated", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");

  await settled(page);

  // Non-featured areas live behind the disclosure, so the page is not 48 cards
  // long by default.
  await page.getByRole("button", { name: /show all/i }).click();

  const row = page.locator("tr", { hasText: "Austria" });
  await expect(row).toBeVisible();
  await expect(row.locator(".seal")).toHaveText("D");
  // The date is the other half of the guard: the grade says "don't trust this",
  // the date says why.
  await expect(row).toContainText("1998");
});

bare("degrades to a message when the interest engine is disabled", async ({ page }) => {
  // `openrate -interest-sources ""` is supported and leaves these routes
  // unrouted. The FX API still answers, so the rest of the page must survive.
  const crashes = watchForCrashes(page);
  await installApi(page);
  await page.route("**/api/v1/interest/**", (route) =>
    route.fulfill({ status: 404, contentType: "text/plain", body: "not found" }));

  await page.goto("/");
  await settled(page, { interest: false });

  await expect(page.locator("#policy .err")).toBeVisible();
  await expect(page.locator("#policy .err")).toContainText(/policy rates unavailable/i);
  // The rest of the app is untouched.
  await expect(page.locator(".instr .result")).toHaveText("1,850.00");
  await expect(page.locator("table.board tr.row")).toHaveCount(Object.keys(RATES.rates).length);
  expect(META.currencies.length, "fixture sanity").toBeGreaterThan(0);

  expect(crashes.pageErrors, "a missing interest engine must not throw").toEqual([]);
});
