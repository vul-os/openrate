#!/usr/bin/env node
// =============================================================================
// scripts/check-shots.mjs — the landing page's screenshots must be the shape
// they are displayed at, and sharp at the widths people read them at.
// Fail-closed.
//
// WHAT WENT WRONG, AND WHY A HUMAN WAS NEVER GOING TO CATCH IT
// ────────────────────────────────────────────────────────────
// The product captures in site/index.html were blurry and silently cropped. The
// cause was two numbers disagreeing in two different files: the capture was
// 1.33 wide (a 1280x960 landscape frame) and the box it was displayed in was
// 0.92 (a portrait 880x960 slot). `object-fit: cover` is defined to resolve
// exactly that disagreement — by throwing away the overflow. So a fifth of every
// capture was discarded, the remainder was enlarged to fill the slot at about
// 1.28 image pixels per CSS pixel, and the page rendered without a warning,
// without an error, and without anything in a diff to point at. "The screenshots
// look a bit soft" was the only signal, and it is not a signal you can act on.
//
// The CSS is now `contain`, so the same disagreement letterboxes instead of
// cropping. That is strictly better and still not good enough: it converts a
// silent failure into a visible one, and "visible" means a human has to look at
// the right page, at the right width, in the right theme, and notice grey bars.
// Nobody does that on a Tuesday. This script is the check that does.
//
// WHAT IT ASSERTS — per displayed capture, per viewport width, per theme
// ─────────────────────────────────────────────────────────────────────
//   1. IT LOADED.        naturalWidth > 0. A capture that 404s hides behind the
//                        page's "Capture unavailable" placeholder, which looks
//                        deliberate. A missing screenshot is a failure.
//   2. SHAPE.            naturalWidth/naturalHeight matches the display box's
//                        own aspect within --tol (default 0.5%). This is the
//                        defect above, stated as an assertion. Under `contain`
//                        the symptom is letterboxing; under `cover` it is a
//                        crop; either way the numbers disagreed first, and this
//                        catches it before either happens.
//   3. DENSITY.          naturalWidth / getBoundingClientRect().width >= --min-
//                        density (default 2.0). A capture displayed at fewer
//                        than 2 image pixels per CSS pixel is visibly soft on
//                        every retina display sold this decade. This is the
//                        "blurry" half of the original defect, and it is a
//                        separate failure from the shape: a correctly shaped
//                        capture taken at 1x is still wrong.
//   4. MARKUP CONTRACT.  The <img> width/height attributes match the file's
//                        real pixel size. The page derives the box's aspect
//                        from those attributes (--ar in the style attribute),
//                        so if they lie about the file, check 2 passes while
//                        the reader still sees letterboxing. This is the check
//                        that makes 2 mean something.
//
// FAIL CLOSED
// ───────────
// Two outcomes: every capture passed every check (exit 0), or a non-zero exit
// naming the capture, the check, the numbers and the width it failed at. There
// is no "playwright isn't installed, skipping" path — a check that skips is a
// check that reports success while verifying nothing, which is how the blurry
// screenshots shipped in the first place. No browser, no pass.
//
// The coverage floor is part of that contract. If the page yields fewer than
// MIN_SHOTS captures, the run fails: a selector that stopped matching (a class
// rename, a markup rewrite) would otherwise pass by checking zero images, and
// that failure mode is indistinguishable in CI from a healthy run.
//
// USAGE
// ─────
//   node scripts/check-shots.mjs                     # check the shipped site/
//   node scripts/check-shots.mjs --selftest          # prove the gate refuses
//   node scripts/check-shots.mjs --min-density 2.5   # tighten the floor
//   node scripts/check-shots.mjs --widths 1440,768   # fewer viewports
//
// Playwright is the only dependency and it is not vendored (this repo ships no
// npm tree). It is found via, in order: --playwright, $PLAYWRIGHT_DIR, the
// repo's own node_modules, a plain `import "playwright"`, then a short list of
// sibling checkouts. If none resolve, the run exits 3 with instructions —
// never 0.
//
// EXIT CODES
// ──────────
//   0  every displayed capture is the right shape and sharp enough
//   2  usage error (unknown flag, unparseable value)
//   3  playwright could not be loaded, or its browser is not installed
//   4  the local server or the page itself failed (load error, console error)
//   5  coverage floor: too few captures were found — the gate verified nothing
//   6  a capture did not load (missing file, decode failure)
//   7  ASPECT DRIFT: the file's shape and its display box's shape disagree
//   8  UNDER-SAMPLED: effective pixel density is below the floor
//   9  the <img> width/height attributes do not match the file's real size
//  10  --selftest: a deliberately broken page was NOT refused
// =============================================================================

import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import path from "node:path";

// ── things a copying repo changes ───────────────────────────────────────────
const SITE_DIR = "site"; // repo-relative root the page is served from
const PAGE = "/index.html";
const SHOT_SELECTOR = ".shotbox img.themeshot"; // the captures the landing displays
const MIN_SHOTS = 2; // coverage floor per (width, theme) pass
const DEFAULT_WIDTHS = [1440, 1280, 768, 390];
const THEMES = ["dark", "light"];
const DEFAULT_TOL = 0.005; // 0.5% aspect agreement
const DEFAULT_MIN_DENSITY = 2.0; // image px per CSS px
const DEFAULT_PORT = 47811;

const E_USAGE = 2;
const E_NO_PLAYWRIGHT = 3;
const E_PAGE = 4;
const E_COVERAGE = 5;
const E_NOT_LOADED = 6;
const E_ASPECT = 7;
const E_DENSITY = 8;
const E_ATTRS = 9;
const E_SELFTEST = 10;

const tty = process.stderr.isTTY;
const RED = tty ? "\x1b[31m" : "";
const GRN = tty ? "\x1b[32m" : "";
const YEL = tty ? "\x1b[33m" : "";
const DIM = tty ? "\x1b[2m" : "";
const BLD = tty ? "\x1b[1m" : "";
const RST = tty ? "\x1b[0m" : "";

// die prints one line per argument — never an embedded "\n" inside a format —
// so a filename or a number echoed back can never be mistaken for markup.
function die(code, ...lines) {
  for (const l of lines) process.stderr.write(`${RED}${l}${RST}\n`);
  process.exit(code);
}
function note(...lines) {
  for (const l of lines) process.stderr.write(`${DIM}${l}${RST}\n`);
}

// ── arguments ───────────────────────────────────────────────────────────────
const argv = process.argv.slice(2);
const opts = {
  selftest: false,
  port: DEFAULT_PORT,
  tol: DEFAULT_TOL,
  minDensity: DEFAULT_MIN_DENSITY,
  widths: DEFAULT_WIDTHS,
  playwright: process.env.PLAYWRIGHT_DIR || "",
};
function needValue(flag, v) {
  if (v === undefined) die(E_USAGE, `${flag} needs a value`);
  return v;
}
function num(flag, v) {
  const n = Number(needValue(flag, v));
  if (!Number.isFinite(n) || n <= 0) die(E_USAGE, `${flag}: ${v} is not a positive number`);
  return n;
}
for (let i = 0; i < argv.length; i++) {
  const a = argv[i];
  switch (a) {
    case "--selftest": opts.selftest = true; break;
    case "--port": opts.port = num(a, argv[++i]); break;
    case "--tol": opts.tol = num(a, argv[++i]); break;
    case "--min-density": opts.minDensity = num(a, argv[++i]); break;
    case "--playwright": opts.playwright = needValue(a, argv[++i]); break;
    case "--widths":
      opts.widths = needValue(a, argv[++i]).split(",").map((w) => num("--widths", w.trim()));
      break;
    case "-h":
    case "--help":
      process.stdout.write(
        [
          "usage: node scripts/check-shots.mjs [options]",
          "",
          "  --selftest             break the page on purpose and prove the gate refuses",
          "  --widths 1440,768      viewport widths to check (default 1440,1280,768,390)",
          "  --tol 0.005            aspect agreement tolerance (default 0.5%)",
          "  --min-density 2.0      minimum image px per CSS px",
          "  --port 47811           port for the local static server",
          "  --playwright DIR       where to import playwright from ($PLAYWRIGHT_DIR)",
          "",
        ].join("\n"),
      );
      process.exit(0);
      break;
    default:
      die(E_USAGE, `unknown option: ${a}`, "run with --help for the accepted flags");
  }
}

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const siteRoot = path.join(repoRoot, SITE_DIR);

// ── playwright, or nothing ──────────────────────────────────────────────────
// Resolution order is explicit and every failure is fatal. There is deliberately
// no fallback that lets the run continue without a browser.
async function loadPlaywright() {
  const require = createRequire(import.meta.url);
  const candidates = [];
  if (opts.playwright) candidates.push(opts.playwright);
  candidates.push(
    path.join(repoRoot, "node_modules", "playwright"),
    "playwright",
    "/Users/pc/code/vulos/athar/node_modules/playwright",
  );
  const tried = [];
  for (const c of candidates) {
    try {
      const spec = c.startsWith("/") || c.startsWith(".")
        ? pathToFileURL(require.resolve(c)).href
        : c;
      const mod = await import(spec);
      const chromium = mod.chromium || (mod.default && mod.default.chromium);
      if (chromium) return { chromium, from: c };
      tried.push(`${c} (loaded, but exports no chromium)`);
    } catch (e) {
      tried.push(`${c} (${e.code || e.message})`);
    }
  }
  die(
    E_NO_PLAYWRIGHT,
    "playwright could not be loaded, so NOTHING was checked.",
    "This gate does not skip: a screenshot check that reports success without a browser",
    "is how the blurry captures shipped in the first place.",
    "Tried:",
    ...tried.map((t) => `  - ${t}`),
    "Fix it with one of:",
    "  npm install --no-save playwright && npx playwright install chromium",
    "  PLAYWRIGHT_DIR=/path/to/node_modules/playwright node scripts/check-shots.mjs",
  );
}

// ── a static server for site/, scoped to site/ ──────────────────────────────
const MIME = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".webp": "image/webp",
  ".woff2": "font/woff2",
  ".txt": "text/plain; charset=utf-8",
  ".md": "text/markdown; charset=utf-8",
};

function serve(port) {
  return new Promise((resolve, reject) => {
    const server = createServer(async (req, res) => {
      const url = new URL(req.url, "http://localhost");
      let rel = decodeURIComponent(url.pathname);
      if (rel.endsWith("/")) rel += "index.html";
      const file = path.join(siteRoot, rel);
      // Never serve outside site/: the page under test must not be able to
      // reach the rest of the repo.
      if (!file.startsWith(siteRoot + path.sep)) {
        res.writeHead(403).end("forbidden");
        return;
      }
      try {
        const body = await readFile(file);
        res.writeHead(200, { "Content-Type": MIME[path.extname(file)] || "application/octet-stream" });
        res.end(body);
      } catch {
        res.writeHead(404, { "Content-Type": "text/plain" }).end("not found");
      }
    });
    server.on("error", reject);
    server.listen(port, "127.0.0.1", () => resolve(server));
  });
}

// ── the measurement, taken in the page ──────────────────────────────────────
// Returns one record per displayed capture: what the file is, what the box is,
// and what the markup claims. All three have to agree.
const MEASURE = (selector) =>
  Array.from(document.querySelectorAll(selector)).map((img) => {
    const r = img.getBoundingClientRect();
    const box = img.closest(".shotbox") || img.parentElement;
    const br = box.getBoundingClientRect();
    return {
      src: (img.getAttribute("src") || "").split("/").pop() || "(no src)",
      shot: img.dataset.shot || "(unnamed)",
      complete: img.complete,
      naturalWidth: img.naturalWidth,
      naturalHeight: img.naturalHeight,
      attrWidth: Number(img.getAttribute("width")) || 0,
      attrHeight: Number(img.getAttribute("height")) || 0,
      rectWidth: r.width,
      rectHeight: r.height,
      boxWidth: br.width,
      boxHeight: br.height,
      hidden: img.hidden || getComputedStyle(img).display === "none",
    };
  });

const fail = [];
function record(code, ...lines) {
  fail.push({ code, lines });
}

async function measure(page, url, width, theme) {
  const consoleErrors = [];
  page.on("console", (m) => { if (m.type() === "error") consoleErrors.push(m.text()); });
  page.on("pageerror", (e) => consoleErrors.push(String(e)));

  await page.setViewportSize({ width, height: 900 });
  await page.addInitScript((t) => {
    try { localStorage.setItem("or-theme", t); } catch (e) {}
  }, theme);
  const resp = await page.goto(url, { waitUntil: "load", timeout: 30_000 });
  if (!resp || !resp.ok()) {
    die(E_PAGE, `${PAGE} did not load at ${width}px (${resp ? resp.status() : "no response"})`);
  }
  // The captures are painted by site.js after it reads the theme, so wait for
  // the img elements to actually finish rather than for a timer.
  await page.waitForFunction(
    (sel) => Array.from(document.querySelectorAll(sel)).every((i) => i.complete),
    SHOT_SELECTOR,
    { timeout: 30_000 },
  ).catch(() => {});
  const shots = await page.evaluate(MEASURE, SHOT_SELECTOR);
  if (consoleErrors.length) {
    die(E_PAGE, `console errors at ${width}px / ${theme}:`, ...consoleErrors.map((e) => `  ${e}`));
  }
  return shots;
}

function checkPass(shots, width, theme, label) {
  const where = `${label}${width}px / ${theme}`;
  if (shots.length < MIN_SHOTS) {
    record(E_COVERAGE,
      `${where}: found ${shots.length} captures matching ${SHOT_SELECTOR} (floor ${MIN_SHOTS}).`,
      "  The selector stopped matching, or the captures were removed. Either way this run",
      "  checked nothing, and a gate that checks nothing must not report success.");
    return { count: shots.length, worstDensity: Infinity };
  }
  let worstDensity = Infinity;
  for (const s of shots) {
    const id = `${s.shot} (${s.src})`;
    if (!s.naturalWidth || !s.naturalHeight) {
      record(E_NOT_LOADED,
        `${where}: ${id} did NOT load — naturalWidth=${s.naturalWidth}, naturalHeight=${s.naturalHeight}.`,
        "  The page falls back to its \"Capture unavailable\" placeholder, which looks intentional.",
        "  A landing page that shows the running app must actually show it.");
      continue;
    }

    // 4. markup contract, checked first: the box's aspect is derived from these
    //    attributes, so if they lie the shape check is comparing a lie to itself.
    if (s.attrWidth !== s.naturalWidth || s.attrHeight !== s.naturalHeight) {
      record(E_ATTRS,
        `${where}: ${id} markup disagrees with the file.`,
        `  <img width="${s.attrWidth}" height="${s.attrHeight}">, file is ${s.naturalWidth}x${s.naturalHeight}.`,
        "  The display box takes its aspect from these attributes (--ar), so a stale pair",
        "  letterboxes the reader while every other check still passes. Update the markup",
        "  and the --ar beside it whenever a capture is retaken.");
    }

    // 2. shape.
    const fileAR = s.naturalWidth / s.naturalHeight;
    const boxAR = s.boxWidth / s.boxHeight;
    const drift = Math.abs(fileAR - boxAR) / boxAR;
    if (drift > opts.tol) {
      const contained = Math.min(s.boxWidth, s.boxHeight * fileAR);
      const bars = s.boxWidth - contained;
      record(E_ASPECT,
        `${where}: ${id} ASPECT DRIFT — file ${fileAR.toFixed(4)}, display box ${boxAR.toFixed(4)} (${(drift * 100).toFixed(2)}% apart, tolerance ${(opts.tol * 100).toFixed(2)}%).`,
        `  Box ${s.boxWidth.toFixed(1)}x${s.boxHeight.toFixed(1)} CSS px, file ${s.naturalWidth}x${s.naturalHeight}.`,
        `  Under object-fit: contain this letterboxes by about ${Math.abs(bars).toFixed(0)}px; under cover it would`,
        "  silently crop the same amount away, which is the defect this gate exists for.",
        "  Fix the capture or the --ar/width/height pair — do not fix it with object-fit.");
    }

    // 3. density.
    const density = s.rectWidth > 0 ? s.naturalWidth / s.rectWidth : 0;
    worstDensity = Math.min(worstDensity, density);
    if (density < opts.minDensity) {
      record(E_DENSITY,
        `${where}: ${id} UNDER-SAMPLED — ${density.toFixed(2)} image px per CSS px, floor ${opts.minDensity.toFixed(2)}.`,
        `  Displayed ${s.rectWidth.toFixed(1)} CSS px wide from a ${s.naturalWidth}px file.`,
        "  This is what \"the screenshot looks blurry\" is, measured. Recapture at a higher",
        "  device scale factor, or display it smaller.");
    }
  }
  return { count: shots.length, worstDensity };
}

// ── selftest: prove each refusal actually fires ─────────────────────────────
// A gate is only worth having if its refusals are exercised. Each case breaks
// the page in exactly one way, in the browser, and asserts the check refuses it.
const SELFTEST_CASES = [
  {
    name: "aspect drift (the original defect: box shape ≠ file shape)",
    want: E_ASPECT,
    css: ".shotbox { aspect-ratio: 4 / 5 !important; }",
  },
  {
    name: "under-sampled capture (the 2x file displayed at 1x)",
    want: E_DENSITY,
    // Density is file pixels per CSS pixel, so the way to break it without
    // touching the files is to enlarge the slot. aspect-ratio is left alone, so
    // this case fails ONLY the density check — it does not pass by accident on
    // the back of the aspect one.
    css: ".shotbox { width: 4000px !important; }",
  },
  {
    name: "missing capture (a 404 behind the placeholder)",
    want: E_NOT_LOADED,
    route: true,
  },
  {
    name: "stale width/height attributes (the box's --ar derived from a lie)",
    want: E_ATTRS,
    js: (sel) => document.querySelectorAll(sel).forEach((i) => i.setAttribute("width", String(i.naturalWidth + 7))),
  },
  {
    name: "captures gone / selector renamed (the gate checking nothing)",
    want: E_COVERAGE,
    js: (sel) => document.querySelectorAll(sel).forEach((i) => i.classList.remove("themeshot")),
  },
];

async function runSelftest(chromium, url) {
  const browser = await chromium.launch();
  let refused = 0;
  for (const c of SELFTEST_CASES) {
    const page = await browser.newPage();
    if (c.route) await page.route("**/assets/app/*.webp", (r) => r.abort());
    await page.setViewportSize({ width: c.viewport || 1440, height: 900 });
    await page.goto(url, { waitUntil: "load", timeout: 30_000 });
    if (c.css) await page.addStyleTag({ content: c.css });
    await page.waitForTimeout(400);
    if (c.js) await page.evaluate(c.js, SHOT_SELECTOR);
    const shots = await page.evaluate(MEASURE, SHOT_SELECTOR);
    await page.close();

    fail.length = 0;
    checkPass(shots, c.viewport || 1440, "dark", "selftest: ");
    const got = fail.map((f) => f.code);
    if (!got.includes(c.want)) {
      await browser.close();
      die(
        E_SELFTEST,
        `SELFTEST FAILED: "${c.name}" was NOT refused.`,
        `  expected exit code ${c.want}, the check raised: ${got.length ? got.join(", ") : "nothing at all"}`,
        "  The gate has stopped detecting the thing it was written for. From a green run that",
        "  is indistinguishable from a healthy page, which is precisely the situation this",
        "  selftest exists to prevent. Do not silence it — repair the check.",
      );
    }
    refused++;
    process.stderr.write(`${GRN}  refused${RST} ${c.name} ${DIM}(exit ${c.want})${RST}\n`);
  }
  fail.length = 0;
  await browser.close();
  if (refused !== SELFTEST_CASES.length) {
    die(E_SELFTEST, `only ${refused} of ${SELFTEST_CASES.length} selftest cases ran`);
  }
  process.stderr.write(`${GRN}${BLD}selftest: ${refused}/${SELFTEST_CASES.length} deliberate breakages refused${RST}\n`);
}

// ── main ────────────────────────────────────────────────────────────────────
const { chromium, from } = await loadPlaywright();
note(`playwright ${from}`);

let server;
try {
  server = await serve(opts.port);
} catch (e) {
  die(E_PAGE, `could not serve ${SITE_DIR}/ on 127.0.0.1:${opts.port}: ${e.message}`,
    "pick another port with --port");
}
const url = `http://127.0.0.1:${opts.port}${PAGE}`;
note(`serving ${SITE_DIR}/ at ${url}`);

let browser;
try {
  try {
    browser = await chromium.launch();
  } catch (e) {
    die(E_NO_PLAYWRIGHT,
      `chromium would not launch: ${e.message.split("\n")[0]}`,
      "install the browser binary:  npx playwright install chromium");
  }

  if (opts.selftest) {
    await runSelftest(chromium, url);
  }

  const rows = [];
  for (const width of opts.widths) {
    for (const theme of THEMES) {
      const page = await browser.newPage({ deviceScaleFactor: 1 });
      const shots = await measure(page, url, width, theme);
      await page.close();
      const { count, worstDensity } = checkPass(shots, width, theme, "");
      rows.push({ width, theme, count, worstDensity });
    }
  }

  process.stderr.write("\n");
  for (const r of rows) {
    const d = r.worstDensity === Infinity ? "—" : `${r.worstDensity.toFixed(2)}x`;
    const flag = r.worstDensity < opts.minDensity ? `${RED}` : `${GRN}`;
    process.stderr.write(
      `  ${String(r.width).padStart(5)}px ${r.theme.padEnd(6)} ${r.count} captures  ` +
      `worst density ${flag}${d}${RST}\n`,
    );
  }

  if (fail.length) {
    process.stderr.write("\n");
    for (const f of fail) for (const l of f.lines) process.stderr.write(`${RED}${l}${RST}\n`);
    const code = fail[0].code;
    process.stderr.write(
      `\n${RED}${BLD}FAILED${RST} ${RED}${fail.length} screenshot check(s). ` +
      `Exiting ${code}.${RST}\n`,
    );
    process.exit(code);
  }

  const passes = rows.length;
  const total = rows.reduce((n, r) => n + r.count, 0);
  process.stderr.write(
    `\n${GRN}${BLD}OK${RST} ${GRN}${total} capture checks across ${passes} viewport/theme passes: ` +
    `every capture is its display box's shape (within ${(opts.tol * 100).toFixed(2)}%) ` +
    `and at least ${opts.minDensity.toFixed(1)}x sampled.${RST}\n`,
  );
} finally {
  if (browser) await browser.close().catch(() => {});
  if (server) await new Promise((r) => server.close(r));
}
