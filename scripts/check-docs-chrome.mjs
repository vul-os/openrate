#!/usr/bin/env node
/* scripts/check-docs-chrome.mjs — the suite chrome rules, checked here.
 *
 * WHY THIS EXISTS, AND WHAT IT IS NOT
 * ───────────────────────────────────
 * The suite's canonical chrome gate is
 * `vulos-cloud/scripts/check-suite-chrome.mjs`. It is the authority on the four
 * ratified Vulos-chrome rules (one logo-only Vulos element in the top bar, one
 * .vulos-foot line in the landing footer, "Vulos" nowhere else in the visible
 * body, no licence text in the footer) plus 2b, no <footer> on a docs page. Run
 * it, do not reimplement it:
 *
 *     cd ../vulos-cloud && node scripts/check-suite-chrome.mjs
 *
 * This file exists for the part that gate does not cover, and for one part it
 * does — because it lives in another repository and therefore never runs in
 * THIS repository's CI. That gap is not hypothetical: slipscan's docs footer
 * was correctly removed, silently reinstated by a later redesign, and stayed
 * broken for weeks because the only check that would have caught it ran
 * somewhere else.
 *
 * So, deliberately overlapping by exactly one rule:
 *
 *   • docs.html has no <footer> — the one ratified rule mirrored here, so it
 *     is checked on every push rather than whenever somebody remembers to run
 *     the suite gate. It was removed from this repo in f4871c6 after a
 *     well-meaning consistency pass added it.
 *
 * And, not covered anywhere else:
 *
 *   • A sibling product shipped an external Spline <iframe>, which silently
 *     broke its "makes no outbound calls" claim. openrate makes that claim on
 *     its own landing, in the docs, and in serve/web/embed_test.go. A page
 *     that quietly fetches a font or a script from a CDN makes it false.
 *   • The docs sidebar stopped being pinned when the layout was reworked, and
 *     the shell drifted away from the left edge.
 *   • The sidebar's five groups and the DOCS array's `group` fields are the
 *     same fact written twice.
 *
 * This is deliberately a STATIC check. It parses the shipped bytes; it does not
 * launch a browser (scripts/check-shots.mjs does that, and needs Playwright,
 * which is not installed on every machine that touches this repo). Everything
 * here is a property of the source, so a browser would add nothing but a
 * dependency that can be missing — and a gate that skips when its dependency
 * is missing is the failure mode this file is written against.
 *
 *   node scripts/check-docs-chrome.mjs            # check what ships
 *   node scripts/check-docs-chrome.mjs --selftest # break it seventeen ways, require seventeen failures
 */

import { readFileSync, readdirSync, statSync } from "node:fs";
import { runInNewContext } from "node:vm";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const SITE = join(ROOT, "site");

/* Coverage floors. Each is below what ships today and above zero, so an
 * intentional deletion is allowed and a scan that silently stops walking the
 * tree is not. */
const FLOOR = {
  scannedFiles: 8,      // text files under site/ that the outbound scan reads
  scannedBytes: 100000, // …and their combined size
  navLinks: 13,         // sidebar entries in docs.html
  navGroups: 4,         // sidebar sections
};

/* The only origins site/ is allowed to name. Every one is a link a reader
 * clicks, never a resource the browser fetches — the distinction the outbound
 * scan below is built on. */
const ALLOWED_LINK_HOSTS = [
  "github.com",
  "raw.githubusercontent.com",
  "pkg.go.dev",
  "vulos.org",
  "www.w3.org",             // SVG/XML namespaces, which are identifiers, not fetches
  "schema.org",
  "creativecommons.org",
  "opensource.org",
  "www.apache.org",
  "spdx.org",
  // The data sources, cited on the landing so a reader can go and check the
  // number against the issuer. Links, never fetches — openrate's own claim is
  // that the SITE calls nothing, not that it mentions nobody.
  "www.ecb.europa.eu",
  "www.resbank.co.za",
  "www.bis.org",
  "www.coinbase.com",
  "www.luno.com",
];

/* Attributes whose value the browser FETCHES. An origin in one of these is an
 * outbound call; an origin in an href is a link the reader chooses to follow. */
const FETCHING_ATTRS = ["src", "srcset", "poster", "data", "action", "formaction", "background", "imagesrcset"];

/* ── helpers ────────────────────────────────────────────────────────────── */

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry);
    const st = statSync(p);
    if (st.isDirectory()) walk(p, out);
    else out.push(p);
  }
  return out;
}

const TEXTUAL = /\.(html|css|js|mjs|svg|json|txt|md)$/i;

function readSite() {
  const files = [];
  for (const p of walk(SITE)) {
    if (!TEXTUAL.test(p)) continue;
    files.push({ name: relative(SITE, p).split("\\").join("/"), text: readFileSync(p, "utf8") });
  }
  return files;
}

/* ── the check ──────────────────────────────────────────────────────────── */

/**
 * @param {{name:string,text:string}[]} files  everything textual under site/
 * @returns {string[]} failures
 */
export function check(files) {
  const failures = [];
  const fail = (m) => failures.push(m);
  const byName = Object.fromEntries(files.map((f) => [f.name, f.text]));

  const bytes = files.reduce((n, f) => n + f.text.length, 0);
  if (files.length < FLOOR.scannedFiles) fail(`only ${files.length} text files were read under site/ (floor ${FLOOR.scannedFiles}) — the scan verified almost nothing`);
  if (bytes < FLOOR.scannedBytes) fail(`only ${bytes} bytes were read under site/ (floor ${FLOOR.scannedBytes}) — the scan verified almost nothing`);

  const docs = byName["docs.html"];
  const index = byName["index.html"];
  const css = byName["assets/site.css"];
  if (docs === undefined) return [...failures, "site/docs.html is missing — every docs assertion below verified NOTHING"];
  if (index === undefined) return [...failures, "site/index.html is missing — every landing assertion below verified NOTHING"];
  if (css === undefined) return [...failures, "site/assets/site.css is missing — every layout assertion below verified NOTHING"];

  /* 1 ── no footer on the docs page. Ratified, previously mutation-tested,
   *      and it has regressed in this suite before. */
  if (/<footer[\s>]/i.test(docs)) {
    fail("site/docs.html contains a <footer> — docs pages carry no footer in this suite; the chrome belongs on the landing (see commit f4871c6)");
  }
  // The landing is the page that DOES carry it, so an assertion that only ever
  // looks at docs.html can pass because footers were abolished everywhere. If
  // the landing loses its footer this rule has stopped meaning anything.
  if (!/<footer[\s>]/i.test(index)) {
    fail("site/index.html has no <footer> — the docs no-footer rule is only meaningful while the landing still has one, so this is a broken control, not a saving");
  }

  /* 2 ── the docs sidebar is pinned, and the shell is packed left. */
  const sideBlock = ruleBody(css, ".docs-side");
  if (!sideBlock) fail("site/assets/site.css has no `.docs-side` rule — the sidebar layout could not be checked at all");
  else {
    if (!/position:\s*sticky/.test(sideBlock)) fail("`.docs-side` is not `position: sticky` — the docs index must stay pinned while the document scrolls");
    if (!/top:\s*\d/.test(sideBlock)) fail("`.docs-side` is sticky with no `top` — a sticky element with no offset never sticks");
    if (!/height:\s*calc\(100dvh/.test(sideBlock)) fail("`.docs-side` no longer takes the full viewport height — the rail's rule stops partway down the page and reads as a box, not an edge");
    if (!/padding:[^;]*var\(--gut\)/.test(sideBlock)) fail("`.docs-side` no longer pads with --gut on the left — the shell is packed to the viewport's left edge, and --gut is what keeps the text off it");
  }
  const gridBlock = ruleBody(css, ".docs");
  if (!gridBlock) fail("site/assets/site.css has no `.docs` grid rule — the shell layout could not be checked");
  else {
    if (!/grid-template-columns:\s*256px/.test(gridBlock)) {
      fail("the `.docs` grid no longer leads with a fixed index track — the shell must be packed left, index first");
    }
    // A bare `1fr` track takes its automatic minimum from its content's
    // min-content width, which is how a non-wrapping child drags the whole page
    // into horizontal scroll. `overflow-x: clip` on <body> then HIDES that from
    // any scrollWidth-based check, which is exactly how a sibling landing
    // overhung its viewport twice over without a gate noticing.
    if (/grid-template-columns:[^;]*(?<![-\w(])1fr/.test(gridBlock.replace(/minmax\(0,\s*1fr\)/g, "minmax0"))) {
      fail("the `.docs` grid uses a bare `1fr` track — use `minmax(0, 1fr)`; a bare 1fr's automatic minimum is its content's min-content size and takes the page into horizontal scroll");
    }
  }

  /* 3 ── nothing under site/ names an origin the browser would FETCH. */
  let originsSeen = 0;
  for (const f of files) {
    if (f.name.endsWith(".txt") || f.name.endsWith(".md")) continue; // licence text and generated prose: read, never fetched
    // Fetching attributes with an absolute or protocol-relative target.
    for (const m of f.text.matchAll(new RegExp(`\\b(${FETCHING_ATTRS.join("|")})\\s*=\\s*["']((?:https?:)?//[^"']+)["']`, "gi"))) {
      fail(`site/${f.name}: \`${m[1]}="${m[2]}"\` is an outbound fetch — every asset this site serves must ship in site/`);
    }
    // <link href> is a fetch for some rel values and pure metadata for others:
    // rel="stylesheet"/"preload"/"icon" pull bytes, rel="canonical" is a
    // statement about this page's address and MUST be absolute. Matching on
    // the tag alone flags every canonical in the bundle, so match on the rel.
    for (const m of f.text.matchAll(/<link\b([^>]*)>/gi)) {
      const attrs = m[1];
      const rel = (attrs.match(/\brel\s*=\s*["']([^"']+)["']/i) || [])[1] || "";
      if (!/\b(stylesheet|preload|modulepreload|prefetch|preconnect|dns-prefetch|icon|apple-touch-icon|manifest)\b/i.test(rel)) continue;
      const href = (attrs.match(/\bhref\s*=\s*["']((?:https?:)?\/\/[^"']+)["']/i) || [])[1];
      if (href) fail(`site/${f.name}: \`<link rel="${rel}" href="${href}">\` is an outbound fetch — a stylesheet, font or icon must ship in site/`);
    }
    // @import and url() in CSS, and any iframe at all.
    for (const m of f.text.matchAll(/@import\s+(?:url\()?["']?((?:https?:)?\/\/[^"')\s]+)/gi)) {
      fail(`site/${f.name}: \`@import ${m[1]}\` is an outbound fetch`);
    }
    for (const m of f.text.matchAll(/url\(\s*["']?((?:https?:)?\/\/[^"')\s]+)/gi)) {
      fail(`site/${f.name}: \`url(${m[1]})\` is an outbound fetch`);
    }
    if (/<iframe[\s>]/i.test(f.text)) {
      fail(`site/${f.name} contains an <iframe> — an embedded document is an outbound call by another name; a sibling product broke exactly this claim with a Spline embed`);
    }
    // Anything else naming an origin must be a link to a host on the list.
    for (const m of f.text.matchAll(/https?:\/\/([A-Za-z0-9.-]+)/g)) {
      originsSeen++;
      if (!ALLOWED_LINK_HOSTS.includes(m[1])) {
        fail(`site/${f.name} names the origin ${m[1]}, which is not on the allow-list in scripts/check-docs-chrome.mjs — add it with a reason, or drop it`);
      }
    }
  }
  if (originsSeen < 5) fail(`only ${originsSeen} origins were seen anywhere in site/ (floor 5) — the outbound scan is not matching and verified nothing`);

  /* 4 ── the docs nav is real: every DOCS slug has a sidebar link, and the
   *      sidebar has enough entries to be an index rather than a stub. The
   *      Go-side gate (site/gen/gen_test.go) already ties DOCS to the
   *      generator; this ties the visible markup to DOCS. */
  const navSlugs = [...docs.matchAll(/data-slug="([\w-]+)"/g)].map((m) => m[1]);
  if (navSlugs.length < FLOOR.navLinks) {
    fail(`site/docs.html sidebar carries ${navSlugs.length} links (floor ${FLOOR.navLinks}) — the nav scan is broken, or the index has been gutted`);
  }
  const docsArray = docs.match(/const DOCS = (\[[\s\S]*?\]);/);
  if (!docsArray) fail("site/docs.html has no `const DOCS = [...]` — the viewer's nav contract is gone and nothing downstream can check it");
  else {
    let parsed;
    try { parsed = JSON.parse(docsArray[1]); } catch (e) { fail(`site/docs.html: DOCS is not JSON-parseable (${e.message}) — site/gen/gen_test.go parses it and would fail too`); }
    if (parsed) {
      for (const d of parsed) {
        if (!navSlugs.includes(d.slug)) fail(`site/docs.html: DOCS lists "${d.slug}" but no sidebar link has data-slug="${d.slug}"`);
      }
      for (const s of navSlugs) {
        if (!parsed.some((d) => d.slug === s)) fail(`site/docs.html: a sidebar link has data-slug="${s}", which DOCS does not list — clicking it loads the overview instead`);
      }

      /* 4b ── the grouping is stated twice and must agree. The sidebar's
       * <section data-group> markup is what a reader without JavaScript sees;
       * DOCS[].group is what the search results are labelled with. Drift shows
       * up as a result card filed under a heading the page is not under. */
      const sections = [...docs.matchAll(/<section class="nav-group" data-group="([^"]+)">([\s\S]*?)<\/section>/g)];
      if (sections.length < FLOOR.navGroups) {
        fail(`site/docs.html has ${sections.length} <section class="nav-group"> blocks (floor ${FLOOR.navGroups}) — the group scan is broken, or the index has been flattened`);
      }
      const groupOf = {};
      for (const [, name, body] of sections) {
        for (const m of body.matchAll(/data-slug="([\w-]+)"/g)) groupOf[m[1]] = name;
      }
      for (const d of parsed) {
        if (!d.group) { fail(`site/docs.html: DOCS entry "${d.slug}" has no \`group\` — the sidebar files it somewhere, and the search results would label it with nothing`); continue; }
        if (groupOf[d.slug] === undefined) { fail(`site/docs.html: "${d.slug}" is not inside any <section class="nav-group"> — it would render outside every group`); continue; }
        if (groupOf[d.slug] !== d.group) {
          fail(`site/docs.html: DOCS says "${d.slug}" is in group "${d.group}", but its sidebar link sits under "${groupOf[d.slug]}"`);
        }
      }
    }
  }

  /* 5 ── every fenced language the corpus uses is in the vendored bundle.
   *
   * highlight.js here is a CUSTOM build carrying only the grammars the docs
   * actually fence — that is what keeps it at 34 KB instead of 1.2 MB — and
   * the failure mode of a custom build is silent: hljs falls back to
   * plaintext, the block renders flat grey, and it reads as a palette problem
   * rather than a missing grammar. It happened here: docs/c-abi.md and
   * docs/quickstart.md added `c` and `python` fences to a bundle that had
   * neither.
   *
   * So the bundle is EVALUATED, not grepped: a regex over minified source
   * would answer a question about the file's text rather than about what the
   * browser will actually be able to highlight. */
  const bundle = byName["assets/vendor/highlight.min.js"];
  if (bundle === undefined) fail("site/assets/vendor/highlight.min.js is missing — every code block would render unhighlighted, and the fence-language scan verified NOTHING");
  else {
    let hljs = null;
    try {
      const sandbox = { window: {} };
      sandbox.window.window = sandbox.window;
      runInNewContext(bundle, sandbox.window);
      hljs = sandbox.window.hljs;
    } catch (e) {
      fail(`site/assets/vendor/highlight.min.js could not be evaluated (${e.message}) — it would not run in a browser either`);
    }
    if (hljs && typeof hljs.getLanguage !== "function") fail("the vendored highlight.js bundle does not expose window.hljs.getLanguage — nothing would be highlighted");
    else if (hljs) {
      const fences = new Map();          // language -> first file that fences it
      for (const f of files) {
        if (!/^docs\/.*\.md$/.test(f.name) && !f.name.endsWith(".html")) continue;
        // ```lang in markdown, and class="language-lang" in the landing's
        // hand-written code plates.
        for (const m of f.text.matchAll(/^\s*(?:```|~~~)([A-Za-z0-9+#_-]+)/gm)) if (!fences.has(m[1])) fences.set(m[1], f.name);
        for (const m of f.text.matchAll(/class="language-([A-Za-z0-9+#_-]+)"/g)) if (!fences.has(m[1])) fences.set(m[1], f.name);
      }
      if (fences.size < 3) fail(`only ${fences.size} fenced languages were found across the corpus (floor 3) — the fence scan is broken and verified nothing`);
      for (const [lang, where] of fences) {
        if (!hljs.getLanguage(lang)) {
          fail(
            `site/${where} fences \`${lang}\`, which the vendored highlight.js bundle does not carry — that block renders as flat plaintext. ` +
            `Rebuild the bundle with the ${lang} grammar (see site/assets/vendor/highlight.min.js.LICENSE), or stop fencing it.`
          );
        }
      }
    }
  }

  /* 6 ── reduced motion is honoured, on both pages' shared stylesheet. */
  if (!/@media\s*\(prefers-reduced-motion:\s*reduce\)/.test(css)) {
    fail("site/assets/site.css has no `prefers-reduced-motion: reduce` block — the docs animate scroll, reveals and the guilloché");
  }

  return failures;
}

/** The declaration block of the first top-level rule whose selector is exactly `sel`. */
function ruleBody(css, sel) {
  const src = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const re = new RegExp(`(^|\\})\\s*${sel.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*\\{`, "m");
  const m = re.exec(src);
  if (!m) return null;
  const start = m.index + m[0].length;
  let depth = 1;
  for (let i = start; i < src.length; i++) {
    if (src[i] === "{") depth++;
    else if (src[i] === "}" && --depth === 0) return src.slice(start, i);
  }
  return null;
}

/* ── self-test ──────────────────────────────────────────────────────────── */

function selftest() {
  const base = readSite();
  const edit = (name, fn) => base.map((f) => (f.name === name ? { ...f, text: fn(f.text) } : { ...f }));

  const cases = [
    ["docs.html grows a footer", edit("docs.html", (t) => t.replace("</body>", "<footer class='ftr'>hi</footer></body>")), /docs\.html contains a <footer>/],
    ["the landing loses its footer, making the rule vacuous", edit("index.html", (t) => t.replace(/<footer/gi, "<div").replace(/<\/footer>/gi, "</div>")), /broken control/],
    ["the sidebar stops being pinned", edit("assets/site.css", (t) => t.replace(".docs-side {\n  position: sticky;", ".docs-side {\n  position: static;")), /not `position: sticky`/],
    ["the shell stops being packed left", edit("assets/site.css", (t) => t.replace("grid-template-columns: 256px minmax(0, 1fr) clamp(232px, 18vw, 300px);", "grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) clamp(232px, 18vw, 300px);")), /packed left/],
    ["a bare 1fr comes back into the docs grid", edit("assets/site.css", (t) => t.replace("grid-template-columns: 256px minmax(0, 1fr) clamp(232px, 18vw, 300px);", "grid-template-columns: 256px 1fr clamp(232px, 18vw, 300px);")), /bare `1fr` track/],
    ["a CDN font sneaks into the stylesheet", edit("assets/site.css", (t) => "@import url(https://fonts.googleapis.com/css2?family=Inter);\n" + t), /outbound fetch/],
    ["a remote script is added to the docs page", edit("docs.html", (t) => t.replace("</body>", '<script src="https://cdn.jsdelivr.net/npm/x"></script></body>')), /outbound fetch/],
    ["a remote stylesheet is linked from the docs page", edit("docs.html", (t) => t.replace("</head>", '<link rel="stylesheet" href="https://fonts.googleapis.com/x"></head>')), /<link rel="stylesheet".*outbound fetch/],
    ["a Spline iframe is embedded in the landing", edit("index.html", (t) => t.replace("</body>", '<iframe src="./local.html"></iframe></body>')), /<iframe>/],
    ["a sidebar link points at a slug DOCS does not serve", edit("docs.html", (t) => t.replace('data-slug="api"', 'data-slug="apu"')), /DOCS does not list/],
    ["a page is filed under a different group in the markup than in DOCS", edit("docs.html", (t) => t.replace('{"slug":"c-abi","title":"Other languages","group":"Embedding"}', '{"slug":"c-abi","title":"Other languages","group":"Help"}')), /sidebar link sits under "Embedding"/],
    ["the groups are flattened away", edit("docs.html", (t) => t.replace(/<section class="nav-group" data-group="[^"]+">/g, "<div>").replace(/<\/section>/g, "</div>")), /group scan is broken|has been flattened/],
    ["a doc fences a language the bundle does not carry", edit("docs/quickstart.md", (t) => t.replace("```python", "```rust")), /vendored highlight\.js bundle does not carry/],
    ["the highlight bundle is emptied of a grammar the docs use", edit("assets/vendor/highlight.min.js", (t) => t.replace(/registerLanguage\("c"/g, 'registerLanguage("czz"').replace(/"c",/g, '"czz",')), /fences `c`|does not carry/],
    ["the highlight bundle stops being loadable", edit("assets/vendor/highlight.min.js", (t) => "throw new Error('boom');" + t), /could not be evaluated/],
    ["the reduced-motion block is dropped", edit("assets/site.css", (t) => t.replace("@media (prefers-reduced-motion: reduce)", "@media (min-width: 1px)")), /prefers-reduced-motion/],
    ["the site walker stops finding files", [base[0]], /verified almost nothing/],
  ];

  let bad = 0;
  for (const [what, files, want] of cases) {
    const failures = check(files);
    const hit = failures.some((f) => want.test(f));
    console.log(`  ${hit ? "caught" : "MISSED"}  ${what}`);
    if (!hit) {
      bad++;
      console.log(`          expected a failure matching ${want}; got:\n            ${failures.join("\n            ") || "(no failures at all)"}`);
    }
  }
  const failures = check(base);
  if (failures.length) {
    bad++;
    console.log(`  MISSED  the unmutated tree should pass, but:\n            ${failures.join("\n            ")}`);
  } else {
    console.log("  ok      the unmutated tree passes");
  }
  return bad;
}

/* ── main ───────────────────────────────────────────────────────────────── */

if (process.argv.includes("--selftest")) {
  console.log("check-docs-chrome --selftest — break it, and require it to notice:");
  const bad = selftest();
  console.log(bad === 0 ? "\ncheck-docs-chrome: selftest PASS — every mutation was caught" : `\ncheck-docs-chrome: selftest FAIL — ${bad} mutation(s) went unnoticed`);
  process.exit(bad === 0 ? 0 : 1);
} else {
  const files = readSite();
  const failures = check(files);
  if (failures.length) {
    console.error("check-docs-chrome: FAIL\n");
    for (const f of failures) console.error("  • " + f);
    console.error(`\n${failures.length} problem(s).`);
    process.exit(1);
  }
  const bytes = files.reduce((n, f) => n + f.text.length, 0);
  console.log(`check-docs-chrome: PASS — ${files.length} files / ${bytes} bytes under site/, no footer on the docs page, sidebar pinned, no outbound origin`);
}
