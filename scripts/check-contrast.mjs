#!/usr/bin/env node
/* scripts/check-contrast.mjs — the muted text tiers, measured rather than eyeballed.
 *
 * WHY THIS EXISTS
 * ───────────────
 * openrate paints text in five tiers (--text … --text-5). The bottom two were
 * chosen as a fade, not as text colours, and they ended up carrying real
 * sentences: the seal-card explainer copy, the run-it step descriptions, the
 * footer nav, the ordered-list markers in the docs, the × and = in the
 * reconciliation equation. Measured, they were between 1.95:1 and 3.80:1
 * against the page — under WCAG 2.2 AA's 4.5:1 floor for body text.
 *
 * That was fixed by hand, and the fix was recorded the way this repo records
 * everything: a comment beside the rule quoting the ratio. A comment is not a
 * check. This file turns those comments into a gate — it recomputes every
 * number from the hex values that actually ship and fails if one of them stops
 * being true.
 *
 * WHAT IT ASSERTS
 * ───────────────
 *   1. Every `color: var(--text-N)` declaration in site/assets/*.css and every
 *      inline `color:var(--text-N)` in site/*.html uses a tier that clears
 *      4.5:1 against ALL THREE page backgrounds (--ink, --ink-2, --paper) in
 *      BOTH themes — or is listed in EXCEPTIONS below.
 *   2. An exception is a claim, not a waiver. `nonText` says the declaration
 *      paints something that is not text and must say what. `surface` says the
 *      declaration only ever sits on one named background, and the script then
 *      measures the tier against THAT background and still requires 4.5:1.
 *      Either way the exception has to match a declaration that exists: a
 *      stale exception fails, so a rule cannot be deleted and leave a hole
 *      behind that quietly re-permits the colour later.
 *   3. The twelve code-palette roles clear 4.5:1 against --code-bg, which is
 *      the table site.css writes out in prose.
 *
 * WHAT IT CANNOT SEE — read this before trusting a green run
 * ──────────────────────────────────────────────────────────
 * Colour that is not written as colour. This file reads hex values, so
 * `opacity` is invisible to it by construction: --text-2 can clear 4.5:1
 * against --ink in every theme while an ancestor at `opacity: .55` composites
 * that same text onto that same background at half strength. Nothing in any
 * VALUE here is wrong; the pixels are. That is not hypothetical — this gate
 * was green through a nav measuring 3.54:1 on screen. The same hole swallows
 * rgba() and any color-mix() resolving to an alpha.
 *
 * scripts/check-contrast-rendered.mjs closes it, by loading the real pages in
 * a browser and composing opacity, alpha and backdrop the way the compositor
 * does. The two are complements, not alternatives: this one is fast, needs no
 * browser, and pins the token table itself; that one measures what is read.
 *
 * COVERAGE FLOORS
 * ───────────────
 * Every count this walks is asserted against a floor. A parser that stops
 * matching declarations, or a token table that comes back empty, must FAIL —
 * the failure mode this project sees most often is a guard that examines
 * nothing and prints PASS.
 *
 *   node scripts/check-contrast.mjs             # check what ships
 *   node scripts/check-contrast.mjs --report    # ...and print every ratio
 *   node scripts/check-contrast.mjs --selftest  # break it six ways, require six failures
 */

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");

/* ── WCAG 2.x relative luminance and contrast ───────────────────────────── */

function srgb(hex) {
  const h = hex.trim().replace("#", "");
  const full = h.length === 3 ? h.split("").map((c) => c + c).join("") : h;
  if (!/^[0-9a-fA-F]{6}$/.test(full)) throw new Error(`not a hex colour: ${hex}`);
  return [0, 2, 4].map((i) => parseInt(full.slice(i, i + 2), 16) / 255);
}
const linear = (c) => (c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4));
function luminance(hex) {
  const [r, g, b] = srgb(hex).map(linear);
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
export function contrast(a, b) {
  const [l1, l2] = [luminance(a), luminance(b)];
  return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
}
const r2 = (n) => Math.round(n * 100) / 100;

/* ── the floors ─────────────────────────────────────────────────────────── */

const AA_TEXT = 4.5;              // WCAG 2.2 SC 1.4.3, text below 18.66px/24px bold
const PAGE_SURFACES = ["--ink", "--ink-2", "--paper"];
const TEXT_TIERS = ["--text", "--text-2", "--text-3", "--text-4", "--text-5"];

/* Minimum counts. Each is one below what ships today, so an intentional
 * deletion is allowed and a parser that silently stops working is not. */
const FLOOR = {
  tokensPerTheme: 12,
  declarations: 60,
  files: 4,
  codeRoles: 12,
};

/* ── the exceptions, each one a claim this script re-measures ───────────── */

const EXCEPTIONS = [
  // (.meanline and .meanline .tip also paint with --text-5, but as a
  // border-left and a background — a dashed rule and the 7px diamond that caps
  // it. This scan only looks at `color:`, so they never reach here, and that is
  // the right line to draw: a rule is not text at any ratio.)

  // Decorative separator. `<i>·</i>` opens each row of the grade chain and
  // carries no information — the rows are already a list, and the glyph is
  // marked aria-hidden in index.html so a screen reader never announces it.
  // Verified structurally below rather than asserted: HTML_ARIA_HIDDEN.
  {
    file: "landing.css",
    selector: ".chain .step i",
    nonText: "a decorative · separator, aria-hidden in the markup",
    ariaHidden: { file: "index.html", needle: '<i aria-hidden="true">·</i>', min: 5 },
  },

  // Real text, but only ever on one surface, and it clears the floor there.
  // These are the two the hand pass found and annotated; the annotation is now
  // the machine's problem.
  {
    file: "landing.css",
    selector: ".docket .lbl",
    tier: "--text-3",
    surface: "--paper-2",
    why: "the docket sits on --paper-2, where --text-4 measures 4.29:1",
  },
  {
    file: "landing.css",
    selector: ".shotbar .u",
    tier: "--text-3",
    surface: "--paper-3",
    why: "the screenshot bar sits on --paper-3, where --text-4 measures 3.92:1",
  },
];

/* ── parsing ────────────────────────────────────────────────────────────── */

function stripComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

/** Token table for one selector's block, e.g. `:root` or `[data-theme="light"]`. */
function tokensIn(css, selector) {
  const body = blockBody(stripComments(css), selector);
  if (body === null) return null;
  const out = {};
  for (const m of body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)) {
    const v = m[2].trim();
    if (/^#[0-9a-fA-F]{3,8}$/.test(v)) out[m[1]] = v;
  }
  return out;
}

/** The text between the braces of the first top-level rule whose selector matches. */
function blockBody(css, selector) {
  const at = css.indexOf(selector + " {");
  if (at === -1) return null;
  let depth = 0, start = -1;
  for (let i = at; i < css.length; i++) {
    if (css[i] === "{") { if (depth++ === 0) start = i + 1; }
    else if (css[i] === "}") { if (--depth === 0) return css.slice(start, i); }
  }
  return null;
}

/**
 * Every `color: var(--text-N)` in a stylesheet, with the selector it belongs
 * to. Deliberately simple: split on braces and read the selector text that
 * precedes each block. Nested at-rules (media queries) contribute their inner
 * rules, which is what we want — a media query does not change the colour.
 */
function colourDeclarations(name, css) {
  const src = stripComments(css);
  const out = [];
  let cursor = 0;
  const parts = src.split("}");
  for (const part of parts) {
    const brace = part.indexOf("{");
    if (brace === -1) { cursor += part.length + 1; continue; }
    const selector = part.slice(0, brace).split("\n").pop().trim();
    const body = part.slice(brace + 1);
    for (const m of body.matchAll(/(?:^|[;{\s])color\s*:\s*var\((--text(?:-\d)?)\)/g)) {
      // Line numbers are reported against the comment-stripped source, which
      // is what `cursor` indexes; they are a pointer to the rule, not a
      // citation. The selector in the message is the thing to search for.
      out.push({ file: name, selector, tier: m[1], line: lineOf(src, cursor + brace + 1 + m.index) });
    }
    cursor += part.length + 1;
  }
  return out;
}

/** Inline `style="…color:var(--text-N)…"` and `style="color: var(--text-N)"` in HTML. */
function inlineDeclarations(name, html) {
  const out = [];
  for (const m of html.matchAll(/color\s*:\s*var\((--text(?:-\d)?)\)/g)) {
    out.push({ file: name, selector: `inline style @ ${lineOf(html, m.index)}`, tier: m[1], line: lineOf(html, m.index), inline: true });
  }
  return out;
}

const lineOf = (s, i) => s.slice(0, i).split("\n").length;

/* ── the check ──────────────────────────────────────────────────────────── */

/**
 * @param {{name:string,text:string}[]} files
 * @returns {{failures:string[], rows:object[], counts:object}}
 */
export function check(files, { report = false } = {}) {
  const failures = [];
  const rows = [];
  const fail = (m) => failures.push(m);

  const byName = Object.fromEntries(files.map((f) => [f.name, f.text]));
  if (files.length < FLOOR.files) {
    fail(`only ${files.length} files were read (floor ${FLOOR.files}) — this check examined almost nothing`);
    return { failures, rows, counts: {} };
  }

  const siteCss = byName["site.css"];
  if (!siteCss) { fail("site.css was not read — the token table comes from it and nothing could be measured"); return { failures, rows, counts: {} }; }

  const dark = tokensIn(siteCss, ":root");
  const lightOverride = tokensIn(siteCss, '[data-theme="light"]');
  if (!dark || !lightOverride) {
    fail("could not parse the :root / [data-theme=\"light\"] token tables out of site.css — the whole check verified NOTHING");
    return { failures, rows, counts: {} };
  }
  const light = { ...dark, ...lightOverride };
  for (const [name, t] of [["dark", dark], ["light", light]]) {
    const n = Object.keys(t).length;
    if (n < FLOOR.tokensPerTheme) fail(`${name} theme parsed only ${n} colour tokens (floor ${FLOOR.tokensPerTheme}) — the token scan is broken`);
  }
  const THEMES = { dark, light };

  // Every tier must exist in both themes, or a `var()` below resolves to nothing.
  for (const tier of TEXT_TIERS) {
    for (const [name, t] of Object.entries(THEMES)) {
      if (!t[tier]) fail(`${tier} is not defined in the ${name} theme`);
    }
  }
  if (failures.length) return { failures, rows, counts: {} };

  /** Worst ratio for a tier over a set of surfaces, across both themes. */
  function worst(tier, surfaces) {
    let min = Infinity, at = null;
    for (const [theme, t] of Object.entries(THEMES)) {
      for (const s of surfaces) {
        if (!t[s]) { fail(`${s} is not defined in the ${theme} theme`); continue; }
        const c = contrast(t[tier], t[s]);
        if (c < min) { min = c; at = `${theme} ${tier} ${t[tier]} on ${s} ${t[s]}`; }
      }
    }
    return { min, at };
  }

  // 1. The tier table itself, for the report.
  for (const tier of TEXT_TIERS) {
    const w = worst(tier, PAGE_SURFACES);
    // Informational. A tier below the floor is not itself a failure — --text-5
    // is a rule colour and is allowed to exist. What fails is USING it as text,
    // which is the declaration scan below.
    rows.push({ what: tier, against: "page backgrounds", ratio: r2(w.min), worstCase: w.at, passes: true, note: w.min < AA_TEXT ? "below AA — usable only as a rule colour" : "" });
  }

  // 2. Every declaration.
  const decls = [];
  for (const f of files) {
    if (f.name.endsWith(".css")) decls.push(...colourDeclarations(f.name, f.text));
    if (f.name.endsWith(".html")) decls.push(...inlineDeclarations(f.name, f.text));
  }
  if (decls.length < FLOOR.declarations) {
    fail(`only ${decls.length} \`color: var(--text-N)\` declarations were found (floor ${FLOOR.declarations}) — the declaration scan is broken and verified nothing`);
  }

  const used = new Set();
  for (const d of decls) {
    const w = worst(d.tier, PAGE_SURFACES);
    if (w.min >= AA_TEXT) continue;

    const ex = EXCEPTIONS.find((e) => e.file === d.file && e.selector === d.selector);
    if (!ex) {
      fail(
        `${d.file}:${d.line} \`${d.selector}\` paints text with ${d.tier}, which measures ` +
        `${r2(w.min)}:1 at worst (${w.at}) — under the ${AA_TEXT}:1 AA floor. Raise the tier, ` +
        `or add an entry to EXCEPTIONS in scripts/check-contrast.mjs saying what it really paints.`
      );
      continue;
    }
    used.add(ex);
    if (ex.nonText) continue;
    fail(`${d.file}:${d.line} \`${d.selector}\` is excepted with a \`surface\` claim, but it paints ${d.tier} — the exception names ${ex.tier}`);
  }

  // 3. Surface-scoped exceptions: measure the claim.
  for (const ex of EXCEPTIONS) {
    if (ex.nonText) continue;
    const hit = decls.find((d) => d.file === ex.file && d.selector === ex.selector);
    if (!hit) {
      fail(`EXCEPTIONS lists ${ex.file} \`${ex.selector}\`, which no longer sets a text colour — drop the stale exception`);
      continue;
    }
    used.add(ex);
    if (hit.tier !== ex.tier) {
      fail(`EXCEPTIONS says ${ex.file} \`${ex.selector}\` uses ${ex.tier}, but it uses ${hit.tier}`);
      continue;
    }
    const w = worst(ex.tier, [ex.surface]);
    rows.push({ what: `${ex.selector} (${ex.tier})`, against: ex.surface, ratio: r2(w.min), worstCase: w.at, passes: w.min >= AA_TEXT });
    if (w.min < AA_TEXT) {
      fail(`${ex.file} \`${ex.selector}\` claims ${ex.surface}, but ${ex.tier} measures ${r2(w.min)}:1 there (${w.at}) — still under ${AA_TEXT}:1`);
    }
  }

  // 4. A non-text exception that nothing uses is a hole waiting to be reopened.
  for (const ex of EXCEPTIONS) {
    if (used.has(ex)) continue;
    const hit = decls.find((d) => d.file === ex.file && d.selector === ex.selector);
    if (!hit) fail(`EXCEPTIONS lists ${ex.file} \`${ex.selector}\`, which matches no declaration — drop the stale exception`);
  }

  // 5. A decorative-glyph exception has to be decorative in the MARKUP too.
  for (const ex of EXCEPTIONS) {
    if (!ex.ariaHidden) continue;
    const html = byName[ex.ariaHidden.file];
    if (html === undefined) { fail(`${ex.ariaHidden.file} was not read — the aria-hidden claim for \`${ex.selector}\` could not be checked`); continue; }
    const n = html.split(ex.ariaHidden.needle).length - 1;
    rows.push({ what: `${ex.selector} aria-hidden`, against: ex.ariaHidden.file, ratio: n, worstCase: `${n} occurrences`, passes: n >= ex.ariaHidden.min });
    if (n < ex.ariaHidden.min) {
      fail(
        `${ex.selector} is excepted as decorative, but ${ex.ariaHidden.file} carries only ${n} of ` +
        `\`${ex.ariaHidden.needle}\` (floor ${ex.ariaHidden.min}) — a glyph a screen reader announces is not decoration`
      );
    }
  }

  // 6. The code palette, which site.css documents as a table of ratios.
  const codeBg = dark["--code-bg"];
  if (!codeBg) fail("--code-bg is not defined — the code-palette table could not be measured");
  else {
    const roles = Object.keys(dark).filter((k) => k.startsWith("--code-") && k !== "--code-bg");
    if (roles.length < FLOOR.codeRoles) fail(`only ${roles.length} code-palette roles found (floor ${FLOOR.codeRoles}) — the palette scan is broken`);
    for (const role of roles) {
      const c = contrast(dark[role], codeBg);
      rows.push({ what: role, against: "--code-bg", ratio: r2(c), worstCase: `${dark[role]} on ${codeBg}`, passes: c >= AA_TEXT });
      if (c < AA_TEXT) fail(`${role} measures ${r2(c)}:1 on --code-bg — under the ${AA_TEXT}:1 AA floor`);
    }
  }

  if (report) {
    for (const r of rows) {
      const mark = r.passes ? "ok  " : "FAIL";
      console.log(`  ${mark} ${String(r.what).padEnd(34)} vs ${String(r.against).padEnd(20)} ${String(r.ratio).padStart(6)}   ${r.worstCase}${r.note ? "   — " + r.note : ""}`);
    }
  }

  return { failures, rows, counts: { declarations: decls.length, tokens: Object.keys(dark).length } };
}

/* ── inputs ─────────────────────────────────────────────────────────────── */

const FILES = [
  ["site.css", "site/assets/site.css"],
  ["landing.css", "site/assets/landing.css"],
  ["index.html", "site/index.html"],
  ["docs.html", "site/docs.html"],
];

function readAll() {
  return FILES.map(([name, rel]) => ({ name, text: readFileSync(join(ROOT, rel), "utf8") }));
}

/* ── self-test: six mutations, six required failures ────────────────────── */

function selftest() {
  const base = readAll();
  const clone = () => base.map((f) => ({ ...f }));
  const edit = (name, fn) => {
    const c = clone();
    const f = c.find((x) => x.name === name);
    f.text = fn(f.text);
    return c;
  };

  const cases = [
    [
      "the old --text-4 comes back",
      edit("site.css", (t) => t.replace("--text-4: #717F92", "--text-4: #616D7E")),
      /--text-4.*under the 4\.5:1 AA floor/s,
    ],
    [
      "a new rule paints prose in --text-5",
      edit("site.css", (t) => t + "\n.md p.footnote { color: var(--text-5); }\n"),
      /\.md p\.footnote/,
    ],
    [
      "an inline style in the landing paints --text-5",
      edit("index.html", (t) => t.replace('color:var(--text-4)', 'color:var(--text-5)')),
      /inline style/,
    ],
    [
      "the decorative separator loses its aria-hidden",
      edit("index.html", (t) => t.replaceAll('<i aria-hidden="true">·</i>', "<i>·</i>")),
      /not decoration/,
    ],
    [
      "an excepted rule is deleted but its exception is left behind",
      edit("landing.css", (t) => t.replace(/^\.docket \.lbl \{[^\n]*$/m, "/* removed */")),
      /stale exception/,
    ],
    [
      "an excepted rule quietly drops back to the tier the exception was written to avoid",
      edit("landing.css", (t) => t.replace(".docket .lbl { color: var(--text-3); }", ".docket .lbl { color: var(--text-4); }")),
      /EXCEPTIONS says .* uses --text-3, but it uses --text-4/,
    ],
    [
      "a code-palette role is dimmed below the floor",
      edit("site.css", (t) => t.replace("--code-comment: #6C7D94", "--code-comment: #3A4552")),
      /--code-comment measures .* under the 4\.5:1 AA floor/,
    ],
    [
      "the declaration scanner stops matching",
      base.map((f) => ({ ...f, text: f.name.endsWith(".css") ? f.text.replaceAll("color: var(--text", "color: var(--nope") : f.text })),
      /declaration scan is broken/,
    ],
  ];

  let bad = 0;
  for (const [what, files, want] of cases) {
    const { failures } = check(files);
    const hit = failures.some((f) => want.test(f));
    console.log(`  ${hit ? "caught" : "MISSED"}  ${what}`);
    if (!hit) {
      bad++;
      console.log(`          expected a failure matching ${want}; got:\n            ${failures.join("\n            ") || "(no failures at all)"}`);
    }
  }
  const { failures } = check(base);
  if (failures.length) {
    bad++;
    console.log(`  MISSED  the unmutated tree should pass, but:\n            ${failures.join("\n            ")}`);
  } else {
    console.log("  ok      the unmutated tree passes");
  }
  return bad;
}

/* ── main ───────────────────────────────────────────────────────────────── */

const args = process.argv.slice(2);
if (args.includes("--selftest")) {
  console.log("check-contrast --selftest — break it, and require it to notice:");
  const bad = selftest();
  console.log(bad === 0 ? "\ncheck-contrast: selftest PASS — every mutation was caught" : `\ncheck-contrast: selftest FAIL — ${bad} mutation(s) went unnoticed`);
  process.exit(bad === 0 ? 0 : 1);
} else {
  const { failures, counts } = check(readAll(), { report: args.includes("--report") });
  if (failures.length) {
    console.error("check-contrast: FAIL\n");
    for (const f of failures) console.error("  • " + f);
    console.error(`\n${failures.length} problem(s).`);
    process.exit(1);
  }
  console.log(`check-contrast: PASS — ${counts.declarations} text-colour declarations, ${counts.tokens} tokens, every one at or above ${AA_TEXT}:1`);
}
