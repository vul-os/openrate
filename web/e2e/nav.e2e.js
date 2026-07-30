/**
 * nav.e2e.js — the navigation has to work at phone width, not just desktop.
 *
 * The defect this file exists for: below 900px the inline links were hidden
 * inside a `.navlinks` strip that scrolled horizontally with its scrollbar
 * suppressed, and the anchor (base-currency) control was `display:none`
 * outright. So on a phone, Accuracy / Docs / Self-host were reachable only by
 * dragging an element with no visible affordance, and the anchor — which the
 * hero copy tells you to "change in the nav" — was simply not there.
 *
 * Both are the kind of regression that a desktop-only suite never sees, so the
 * assertions below are pinned to a 390px viewport.
 *
 * Defects this file catches:
 *   - The menu button not being rendered/visible at phone width, leaving the
 *     secondary sections unreachable again.
 *   - The menu opening but not containing the full set of destinations.
 *   - The anchor control going missing at narrow widths while the copy still
 *     points at it.
 *   - The menu not closing on Escape, on outside click, or on navigating —
 *     a panel that traps the user over the content it covers.
 *   - Two anchor controls being live at once (the nav one and the menu one),
 *     which would let the page disagree with itself about the base currency.
 */

import { test, expect, settled } from "./fixtures.js";

const PHONE = { width: 390, height: 900 };

test.describe("narrow-width navigation", () => {
  test.use({ viewport: PHONE });

  test("every destination is reachable from the menu at phone width", async ({ openrate }) => {
    const { page } = openrate;
    await page.goto("/");
    await settled(page);

    // The inline strip is gone at this width; the disclosure replaces it.
    await expect(page.locator(".navlinks")).toBeHidden();
    const toggle = page.locator(".navtoggle");
    await expect(toggle).toBeVisible();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");

    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");

    const menu = page.locator("#navmenu");
    await expect(menu).toBeVisible();
    for (const label of ["Convert", "Board", "Policy", "Accuracy", "Docs"]) {
      await expect(menu.getByRole("link", { name: label, exact: true })).toBeVisible();
    }
    await expect(menu.getByRole("link", { name: /Self-host/ })).toBeVisible();

    // The anchor the hero copy points at must be here, and must be the only one.
    await expect(menu.locator(".csel-btn")).toBeVisible();
    await expect(page.locator(".nav-anchor")).toBeHidden();
  });

  test("the menu closes on Escape, on an outside click, and on navigating", async ({ openrate }) => {
    const { page } = openrate;
    await page.goto("/");
    await settled(page);
    const toggle = page.locator(".navtoggle");
    const menu = page.locator("#navmenu");

    await toggle.click();
    await expect(menu).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(menu).toBeHidden();

    await toggle.click();
    await expect(menu).toBeVisible();
    await page.mouse.click(12, 700); // well clear of the panel
    await expect(menu).toBeHidden();

    await toggle.click();
    await menu.getByRole("link", { name: "Policy", exact: true }).click();
    await expect(menu).toBeHidden();
  });

  test("changing the anchor from the menu re-queries the board", async ({ openrate }) => {
    const { page } = openrate;
    await page.goto("/");
    await settled(page);

    await page.locator(".navtoggle").click();
    await page.locator("#navmenu .csel-btn").click();
    await page.locator(".csel-opt", { hasText: "USD" }).first().click();

    // The placard reads its base from the same state the request uses.
    await expect(page.locator(".hero-note .num").first()).toHaveText("USD");
  });

  test("the currency name is dropped, never sliced, in the narrow converter", async ({ openrate }) => {
    const { page } = openrate;
    await page.goto("/");
    await settled(page);

    // A field too narrow for flag + code + name used to clip the name
    // mid-glyph. At this width the name is hidden outright instead, so nothing
    // overflows its button.
    const sliced = await page.locator(".leg-row .csel-btn").evaluateAll((els) =>
      els.map((el) => ({
        over: el.scrollWidth > el.clientWidth + 1,
        nameShown: getComputedStyle(el.querySelector(".csel-name")).display !== "none",
      }))
    );
    expect(sliced.length).toBeGreaterThan(0);
    for (const s of sliced) {
      expect(s.over).toBe(false);
      expect(s.nameShown).toBe(false);
    }
  });
});
