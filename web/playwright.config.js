/**
 * Playwright E2E config — openrate web.
 *
 * WHY THIS EXISTS: `vite build` exiting 0 proves nothing about whether the
 * bundle it emitted actually RUNS. Two apps in this suite shipped a blank
 * screen with a green build (an unresolved import that became a module which
 * throws on load; a duplicated React that broke hooks). Nothing here ever
 * loaded the BUILT bundle in a real browser. This suite does.
 *
 * The whole suite drives the PRODUCTION build via `vite preview` — never the
 * dev server — so what chromium executes is byte-for-byte what the Go binary
 * embeds (web/embed.go embeds dist/). The JSON API is mocked in-browser with
 * page.route (see e2e/fixtures.js), so the run is hermetic: no openrate Go
 * binary, no network rate feeds.
 *
 * Prereqs:  npm run build            (produces dist/ — `pretest:e2e` does it)
 *           npx playwright install chromium
 * Run:      npm test
 */

import { defineConfig, devices } from "@playwright/test";

// Uncommon port so a stale preview of another Vulos app on a common port
// (5173/4173) can never be mistaken for openrate. Override with E2E_PORT.
const PORT = process.env.E2E_PORT ? Number(process.env.E2E_PORT) : 47331;
const BASE_URL = `http://localhost:${PORT}`;

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/*.e2e.js",
  timeout: 30_000,
  expect: { timeout: 7_000 },
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: process.env.CI ? [["github"], ["list"]] : [["list"]],
  use: {
    baseURL: BASE_URL,
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    // A stale service worker (from any app previously served on this localhost
    // port) must never shadow the page.route mocks or serve a cached bundle —
    // the point of the suite is to exercise the bundle we just built.
    serviceWorkers: "block",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: `npx vite preview --port ${PORT} --strictPort`,
    url: BASE_URL,
    // Never reuse a server on this port: it might be a different app, and we'd
    // silently test the wrong bundle.
    reuseExistingServer: false,
    timeout: 60_000,
  },
});
