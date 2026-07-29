/**
 * converter.e2e.js — the core user flow, in a real browser, against the built
 * bundle: convert a currency, read the working, and read the rate board.
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
 *     shape drifting from what Board.jsx expects).
 *   - Expanding a row throws. Works.jsx reads rate.path, rate.legs, rate.quotes
 *     and quality.corroboration — a nested shape trivially broken by an API
 *     change, which would crash the whole tree rather than just the row.
 *   - Sorting or filtering the board doing nothing, which is the board's whole
 *     purpose ("which of these numbers should I not lean on").
 *   - The anchor (base currency) selector doesn't re-query the board.
 */

import { test, expect, settled } from "./fixtures.js";

test("converts an amount and reflects the mocked rate", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");
  await settled(page);

  const instr = page.locator(".instr");
  await expect(instr).toBeVisible();

  // Default: 100 USD → ZAR at the mocked 18.5 ⇒ 1,850.00 (money precision).
  await expect(instr.locator(".result")).toHaveText("1,850.00");

  // The verdict seal and the inverse pair — the product's whole differentiator
  // (every rate shown with its path, sources and grade), not decoration.
  await expect(instr.locator(".leg-head .seal")).toHaveText("A");
  await expect(instr.locator(".inverse")).toContainText("1 USD = 18.5");
  await expect(instr.locator(".inverse")).toContainText("1 ZAR = 0.054054");

  // Core interaction: a quick-amount chip must drive a real refetch.
  const convertCall = page.waitForResponse(
    (r) => r.url().includes("/api/v1/convert") && new URL(r.url()).searchParams.get("amount") === "1000",
  );
  await instr.getByRole("button", { name: "1,000", exact: true }).click();
  await convertCall;

  // 1000 × 18.5 = 18,500 — computed from the mock, so a stale render can't pass.
  await expect(instr.locator(".result")).toHaveText("18,500.00");
});

test("shows the working for a conversion", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");
  await settled(page);

  const instr = page.locator(".instr");
  await expect(instr.locator(".result")).toHaveText("1,850.00");

  // "Show the working" mounts <Works/>, which walks rate.path, rate.legs,
  // rate.quotes and quality.corroboration. A shape change there throws inside
  // render and takes the ENTIRE app down to a blank screen — the pageErrors
  // gate in the fixture catches that; these assertions catch it rendering
  // nothing useful.
  await instr.getByRole("button", { name: /show the working/i }).click();

  const works = instr.locator(".works");
  await expect(works).toBeVisible();

  // The path: two currency nodes joined by one hop, the hop carrying its own
  // rate and the source that quoted it.
  await expect(works.locator(".path-node")).toHaveCount(2);
  await expect(works.locator(".path-hop")).toHaveCount(1);
  await expect(works.locator(".path-hop")).toContainText("sarb");
  await expect(works).toContainText("directly quoted");

  // Dispersion: one plotted point per corroborating source, plus the stats.
  await expect(works.locator(".disp-pt")).toHaveCount(2);
  await expect(works).toContainText("Agreement");
  await expect(works).toContainText("spread");

  // ...and it collapses again (the disclosure state is real, not one-way).
  await instr.getByRole("button", { name: /hide the working/i }).click();
  await expect(works).toHaveCount(0);
});

test("renders the live rate board and expands a row's derivation", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");
  await settled(page);

  const board = page.locator("table.board").first();
  await expect(board).toBeVisible();

  // One row per mocked currency, sorted by code — EUR, GBP, USD.
  const rows = board.locator("tr.row");
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText("EUR");
  await expect(rows.nth(2)).toContainText("USD");

  // Grades are struck per row (A for USD/GBP, B for EUR in the fixture).
  await expect(rows.nth(0).locator(".seal")).toHaveText("B");
  await expect(rows.nth(2).locator(".seal")).toHaveText("A");

  // Core interaction: clicking a row expands its full derivation in place.
  await rows.nth(2).click();
  const detail = page.locator(".detail-inner");
  await expect(detail).toBeVisible();
  await expect(detail).toContainText("Path");
  await expect(detail).toContainText("directly quoted");

  // ...and clicking again collapses it.
  await rows.nth(2).click();
  await expect(detail).toHaveCount(0);
});

test("sorting the board by grade reorders the rows", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");
  await settled(page);

  const board = page.locator("table.board").first();
  const rows = board.locator("tr.row");
  await expect(rows).toHaveCount(3);

  // Default order is by code, so the B-graded EUR leads. Sorting by grade must
  // put the A rows first — a dead sort is a dead feature here, because sorting
  // by grade is how you find the numbers not to lean on.
  await board.getByRole("columnheader", { name: /grade/i }).click();
  await expect(rows.nth(0).locator(".seal")).toHaveText("A");
  await expect(rows.nth(2).locator(".seal")).toHaveText("B");

  // Clicking again reverses it.
  await board.getByRole("columnheader", { name: /grade/i }).click();
  await expect(rows.nth(0).locator(".seal")).toHaveText("B");
});

test("filtering the board narrows it to the matching currency", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");
  await settled(page);

  const board = page.locator("table.board").first();
  await expect(board.locator("tr.row")).toHaveCount(3);

  // The filter matches the full name as well as the code — searching "pound"
  // has to find GBP, or the field is useless to anyone who does not already
  // know the ISO codes.
  await page.getByLabel("Filter currencies").fill("pound");
  await expect(board.locator("tr.row")).toHaveCount(1);
  await expect(board.locator("tr.row")).toContainText("GBP");
});

test("changing the anchor currency re-queries the board", async ({ openrate }) => {
  const { page } = openrate;
  await page.goto("/");
  await settled(page);
  await expect(page.locator("table.board tr.row").first()).toBeVisible();

  // The anchor select is the compact one in the nav.
  const ratesCall = page.waitForResponse(
    (r) => r.url().includes("/api/v1/rates") && new URL(r.url()).searchParams.get("base") === "EUR",
  );
  await page.locator(".nav-anchor .csel-btn").click();
  await page.locator(".csel-panel .csel-opt", { hasText: "EUR" }).first().click();
  await ratesCall;

  // The board bar is bound to the anchor — proves the new base reached the UI
  // rather than only the network.
  await expect(page.locator(".board-bar")).toContainText("1 EUR buys");
});
