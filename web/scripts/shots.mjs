/**
 * shots.mjs — capture the landing-page screenshots from the REAL running app.
 *
 * Every image on site/index.html is produced by this script against a live
 * openrate serving live rates. Nothing is mocked and nothing is drawn by hand:
 * if a screenshot on the landing shows a number, that number came out of the
 * engine. Re-run it whenever the UI changes and the landing is current again.
 *
 *   $ go build -o openrate ./cmd/openrate && ./openrate -addr :8412 &
 *   $ npm --prefix web run shots            # → site/assets/shots/*.png
 *
 * Override the target with OPENRATE_URL. The script waits for the first
 * snapshot to land before shooting, so a cold start is fine.
 */
import { chromium } from "@playwright/test";
import { mkdir, readdir, readFile, rm, stat } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";
import { dirname, resolve, join } from "node:path";

const run = promisify(execFile);

const URL_ = process.env.OPENRATE_URL || "http://localhost:8412/";
const OUT = resolve(dirname(fileURLToPath(import.meta.url)), "../../site/assets/shots");

const DESKTOP = { width: 1440, height: 900 };
const MOBILE = { width: 420, height: 880 };

// Retina: the landing renders these at roughly half their pixel width, so 2x
// keeps them sharp on the displays most readers will be on.
const SCALE = 2;

async function openPage(browser, viewport, theme) {
  const ctx = await browser.newContext({ viewport, deviceScaleFactor: SCALE });
  // Set the theme before first paint so no shot catches a theme flip mid-frame.
  await ctx.addInitScript((t) => localStorage.setItem("or-theme", t), theme);
  const page = await ctx.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(String(e)));
  await page.goto(URL_, { waitUntil: "networkidle" });
  // The board only has rows once /rates has resolved; without this the first
  // shot of a cold start is a skeleton.
  await page.waitForSelector(".board tbody tr", { timeout: 20_000 });
  await page.waitForTimeout(1200);
  return { page, ctx, errors };
}

const shot = (page, name, opts = {}) => page.screenshot({ path: `${OUT}/${name}.png`, ...opts });

async function section(page, id, name) {
  await page.evaluate((i) => document.getElementById(i)?.scrollIntoView(), id);
  await page.waitForTimeout(700);
  await shot(page, name);
}

/**
 * Re-encode the captured PNGs as WebP.
 *
 * These are 2x shots of a page carrying a guilloché engraving and a grain
 * overlay — near-photographic content that PNG is the wrong format for. The
 * raw set is ~5 MB, which is more than the whole rest of the landing put
 * together; WebP at q82 brings it under 400 KB with no visible loss at the
 * sizes the page renders them.
 *
 * cwebp is a dev-only tool, like the browser Playwright downloads. If it is
 * missing we keep the PNGs and say so rather than failing the capture — but
 * the landing references .webp, so the shots do need converting before they
 * are committed.
 */
const MAX_W = 1800;

// PNG width from the IHDR chunk — bytes 16..19, big-endian. Cheaper and more
// portable than shelling out to an image tool just to ask how wide a file is.
async function pngWidth(path) {
  const buf = await readFile(path);
  return buf.readUInt32BE(16);
}

async function toWebp() {
  try {
    await run("cwebp", ["-version"]);
  } catch {
    console.warn("cwebp not on PATH — keeping PNGs. Install it (brew install webp) and re-run;\n" +
                 "site/index.html references .webp, so the landing will show broken images until you do.");
    return;
  }
  const files = (await readdir(OUT)).filter((f) => f.endsWith(".png"));
  let before = 0, after = 0;
  for (const f of files) {
    const src = join(OUT, f);
    const dst = src.replace(/\.png$/, ".webp");
    before += (await stat(src)).size;
    // Cap the width at 1800 device px — nothing on the landing renders wider
    // than ~1100 CSS px. Only ever downscale: cwebp -resize is unconditional,
    // so passing it blindly would UPSCALE the 840px-wide mobile shots and make
    // them the largest files in the set.
    const w = await pngWidth(src);
    const resize = w > MAX_W ? ["-resize", String(MAX_W), "0"] : [];
    await run("cwebp", ["-q", "82", ...resize, "-quiet", src, "-o", dst]);
    after += (await stat(dst)).size;
    await rm(src);
  }
  const kb = (n) => `${Math.round(n / 1024)} KB`;
  console.log(`webp: ${files.length} shots, ${kb(before)} → ${kb(after)}`);
}

async function main() {
  await mkdir(OUT, { recursive: true });
  const browser = await chromium.launch();
  const problems = [];

  // ── desktop, ink ────────────────────────────────────────────────────
  {
    const { page, ctx, errors } = await openPage(browser, DESKTOP, "dark");
    await shot(page, "converter-dark");

    // The path visualisation, cropped to the panel — it is the one thing no
    // other rate site shows, so it gets its own image rather than a section.
    await page.getByText("Show the working").click();
    await page.waitForTimeout(600);
    await page.locator(".works").first().scrollIntoViewIfNeeded();
    await page.waitForTimeout(400);
    await page.locator(".works").first().screenshot({ path: `${OUT}/working-dark.png` });
    await page.getByText("Hide the working").click();

    await section(page, "board", "board-dark");

    // A two-hop cross, expanded in place on the board.
    await page.getByRole("row").filter({ hasText: "AED" }).first().click();
    await page.waitForTimeout(600);
    await page.locator(".detail-inner").first().scrollIntoViewIfNeeded();
    await page.waitForTimeout(400);
    await page.locator(".detail-inner").first().screenshot({ path: `${OUT}/path-dark.png` });

    await section(page, "policy", "policy-dark");
    await section(page, "accuracy", "accuracy-dark");

    await page.evaluate(() => { location.hash = "#docs"; });
    await page.waitForTimeout(900);
    await shot(page, "docs-dark");

    problems.push(...errors);
    await ctx.close();
  }

  // ── desktop, banknote paper ─────────────────────────────────────────
  {
    const { page, ctx, errors } = await openPage(browser, DESKTOP, "light");
    await shot(page, "converter-light");
    await section(page, "board", "board-light");
    await section(page, "policy", "policy-light");
    problems.push(...errors);
    await ctx.close();
  }

  // ── mobile ──────────────────────────────────────────────────────────
  {
    const { page, ctx, errors } = await openPage(browser, MOBILE, "dark");
    await shot(page, "mobile-dark");
    await section(page, "board", "mobile-board-dark");
    problems.push(...errors);
    await ctx.close();
  }

  await browser.close();
  await toWebp();

  if (problems.length) {
    // A screenshot of a crashed page still writes a valid PNG, so an uncaught
    // exception has to fail the run loudly or the landing quietly ships a
    // picture of a broken app.
    console.error("page errors during capture:\n" + problems.join("\n"));
    process.exit(1);
  }
  console.log(`shots written to ${OUT}`);
}

main();
