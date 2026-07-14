/**
 * converter.e2e.js — the core user flow, in a real browser, against the built
 * bundle: convert a currency, and read the rate board.
 *
 * The boot guard proves the app is not blank. This proves it is not USELESS —
 * that the primary surface actually binds to the API and reacts to input.
 *
 * Defects this file catches:
 *   - The converter renders but never calls /api/v1/convert (broken effect deps,
 *     a state-wiring regression) — the result would stay at the "—" placeholder.
 *   - The converter calls the API but drops the response (renaming `result` on
 *     either side of the wire silently breaks the number the whole product is for).
 *   - The quick-amount chips don't drive a refetch — a dead interaction.
 *   - The rate board renders no rows despite rates arriving (the `rates` map
 *     shape drifting from what RatesTable expects).
 *   - Expanding a row throws (Calc reads rate.quality.corroboration / legs /
 *     quotes — a nested shape that is trivially broken by an API change, and
 *     which would crash the whole tree, not just the row).
 *   - The anchor (base currency) selector doesn't re-query the board.
 */

import { test, expect } from "./fixtures.js";

test("converts an amount and reflects the mocked rate", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");

  const conv = page.locator("section.conv");
  await expect(conv).toBeVisible();

  // Default: 100 USD → ZAR at the mocked 18.5 ⇒ 1,850.
  await expect(conv.locator(".cf-result")).toHaveText("1,850");

  // The rate detail (grade + inverse + quality tiles) must appear — this is the
  // product's whole differentiator ("every price graded for accuracy").
  await expect(conv.locator(".cf-grade .grade")).toHaveText("A");
  await expect(conv.locator(".inverse")).toContainText("1 USD = 18.5");

  // Core interaction: a quick-amount chip must drive a real refetch.
  const convertCall = page.waitForResponse(
    (r) => r.url().includes("/api/v1/convert") && new URL(r.url()).searchParams.get("amount") === "1000",
  );
  await conv.getByRole("button", { name: "1,000", exact: true }).click();
  await convertCall;

  // 1000 × 18.5 = 18,500 — computed from the mock, so a stale render can't pass.
  await expect(conv.locator(".cf-result")).toHaveText("18,500");
});

test("shows the math for a conversion", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");

  const conv = page.locator("section.conv");
  await expect(conv.locator(".cf-result")).toHaveText("1,850");

  // "show the math" mounts <Calc/>, which walks rate.legs, rate.quotes and
  // quality.corroboration. A shape change there throws inside render and takes
  // the ENTIRE app down to a blank screen — the pageErrors gate in the fixture
  // catches that; these assertions catch it rendering nothing useful.
  await conv.getByRole("button", { name: /show the math/i }).click();

  const math = conv.locator(".math");
  await expect(math).toBeVisible();
  await expect(math.locator(".leg")).toHaveCount(1);
  await expect(math.locator(".qrow")).toHaveCount(2); // two corroborating quotes
  await expect(math).toContainText("Dispersion");
  await expect(math).toContainText("spread");
});

test("renders the live rate board and expands a row's calculation", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");

  const table = page.locator("table.rates");
  await expect(table).toBeVisible();

  // One row per mocked currency, sorted — EUR, GBP, USD.
  const rows = table.locator("tr.rrow");
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText("EUR");
  await expect(rows.nth(2)).toContainText("USD");

  // Grades are rendered per row (A for USD/GBP, B for EUR in the fixture).
  await expect(rows.nth(0).locator(".grade")).toHaveText("B");
  await expect(rows.nth(2).locator(".grade")).toHaveText("A");

  // Core interaction: clicking a row expands its full derivation.
  await rows.nth(2).click();
  const detail = table.locator("tr.rdetail");
  await expect(detail).toBeVisible();
  await expect(detail).toContainText("Calculation");
  await expect(detail).toContainText("directly quoted");

  // ...and clicking again collapses it (the open/closed state is real).
  await rows.nth(2).click();
  await expect(detail).toHaveCount(0);
});

test("changing the anchor currency re-queries the board", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");
  await expect(page.locator("table.rates tr.rrow").first()).toBeVisible();

  // The anchor select is the compact one in the nav.
  const ratesCall = page.waitForResponse(
    (r) => r.url().includes("/api/v1/rates") && new URL(r.url()).searchParams.get("base") === "EUR",
  );
  await page.locator(".nav-anchor .csel-btn").click();
  await page.locator(".csel-panel .csel-opt", { hasText: "EUR" }).first().click();
  await ratesCall;

  // The board heading is bound to the anchor — proves the new base reached the UI.
  await expect(page.getByRole("heading", { name: /All rates, 1\s*EUR/i })).toBeVisible();
});
