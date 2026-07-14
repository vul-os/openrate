/**
 * boot.e2e.js — THE BOOT GUARD.
 *
 * The floor beneath every other test: does the bundle we actually ship RUN?
 *
 * Two apps in this suite shipped a blank screen with `vite build` exiting 0 and
 * a fully green unit suite, because nothing ever loaded the BUILT bundle in a
 * real browser. jsdom/vitest cannot catch those failures: they import from
 * `src/`, so a bad *bundle* (an import the bundler could not resolve and turned
 * into a module that throws on load; a second copy of React that makes every
 * hook call invalid) is invisible to them. Chromium executing dist/ is the only
 * thing that catches it.
 *
 * Defects this file catches, concretely:
 *   - ANY uncaught exception during load/render of the built bundle.
 *   - React mounting nothing (root element left empty) → the blank screen.
 *   - The entry chunk being served as text/html instead of JS (a base-path or
 *     SPA-fallback misconfiguration — the page 200s and silently boots nothing).
 *   - The app crashing (rather than degrading) when the rate API is unreachable,
 *     which is the state of every cold start before the first fetch lands.
 */

import { test, expect } from "./fixtures.js";
import { test as bare } from "@playwright/test";
import { installApi, watchForCrashes } from "./fixtures.js";

test("boots the built bundle in a real browser with no uncaught errors", async ({ openrate }) => {
  const { page, pageErrors, failedRequests } = openrate;

  await page.goto("/");

  // The root element must actually contain a rendered tree. `toBeVisible` alone
  // would pass on an empty <div id="root"></div> in some layouts, so assert on
  // real rendered content: React mounted something.
  const root = page.locator("#root");
  await expect(root).not.toBeEmpty();

  // The primary surface — not just "something rendered", but the RIGHT thing.
  await expect(page.getByRole("heading", { name: /graded for accuracy/i })).toBeVisible();

  expect(pageErrors, "uncaught exception(s) while booting the built bundle").toEqual([]);
  expect(failedRequests, "asset(s) failed to load").toEqual([]);
});

bare("entry chunk is served as JavaScript, not an HTML fallback", async ({ page }) => {
  // A base-path / SPA-fallback bug makes the server answer the entry chunk with
  // index.html at HTTP 200. The browser refuses to execute it as a module, the
  // app never boots, and every naive "page loads" check still passes. Pin the
  // content type of the actual entry script the built index.html references.
  await installApi(page); // keep the run hermetic — no request escapes to a backend
  const types = [];
  page.on("response", (res) => {
    if (/\/assets\/.*\.js$/.test(new URL(res.url()).pathname)) {
      types.push({ url: res.url(), type: res.headers()["content-type"] || "", status: res.status() });
    }
  });

  await page.goto("/");
  await expect(page.locator("#root")).not.toBeEmpty();

  expect(types.length, "no JS chunk was requested — index.html references no entry script").toBeGreaterThan(0);
  for (const t of types) {
    expect(t.status, `${t.url} did not 200`).toBe(200);
    expect(t.type, `${t.url} was not served as JavaScript`).toMatch(/javascript|ecmascript/i);
  }
});

bare("degrades to a visible error, never a blank screen, when the API is down", async ({ page }) => {
  // Cold start / backend outage. App.jsx catches and surfaces `err`. If a future
  // change lets a rejection escape (or renders `undefined.rates`), React unmounts
  // the tree and the user gets a WHITE PAGE while the HTTP response is still 200.
  // This is the production failure mode this repo has never had a guard for.
  const crashes = watchForCrashes(page);
  await page.route("**/api/v1/**", (route) => route.fulfill({ status: 503, body: "upstream down" }));

  await page.goto("/");

  // Shell still renders...
  await expect(page.getByRole("heading", { name: /graded for accuracy/i })).toBeVisible();
  // ...and the failure is surfaced to the user rather than swallowed into a blank.
  await expect(page.locator(".err")).toBeVisible();
  await expect(page.locator("#root")).not.toBeEmpty();

  expect(crashes.pageErrors, "API failure must not produce an uncaught exception").toEqual([]);
});

bare("docs route boots without throwing", async ({ page }) => {
  // The docs view is a separate component tree (Docs.jsx + CodeBlock) reached by
  // the #docs hash route. It is never exercised by the landing render, so a
  // module-level throw or bad import in it would ship unnoticed.
  await installApi(page);
  const crashes = watchForCrashes(page);

  await page.goto("/#docs");

  await expect(page.locator(".docs-page")).toBeVisible();
  await expect(page.getByRole("heading", { name: /quick start/i }).first()).toBeVisible();
  expect(crashes.pageErrors, "uncaught exception(s) on the docs route").toEqual([]);
});
