/* openrate — site behaviour. Shared by index.html and docs.html.
   No dependencies, no network calls. Theme switch, product screenshots,
   the guilloché engraving, scroll reveals, and syntax highlighting. */

(function () {
  "use strict";

  /* ── theme ────────────────────────────────────────────────────────────
     Keyed the same as the app ("or-theme"), so a reader who set the paper
     theme in the converter and then follows a link to the docs does not get
     flipped back to ink. The initial value is applied by an inline script in
     <head> to avoid a flash; this only wires the toggle. */
  var root = document.documentElement;
  function setTheme(t) {
    root.dataset.theme = t;
    try { localStorage.setItem("or-theme", t); } catch (e) { /* private mode */ }
    var m = document.querySelector('meta[name="theme-color"]');
    if (m) m.setAttribute("content", t === "dark" ? "#06090E" : "#F2EFE6");
    document.querySelectorAll("[data-theme-toggle]").forEach(function (b) {
      b.setAttribute("aria-label", t === "dark" ? "Switch to paper theme" : "Switch to ink theme");
      b.innerHTML = t === "dark" ? ICON.sun : ICON.moon;
    });
    paintShots();
  }

  /* ── product screenshots ─────────────────────────────────────────────────
     Real captures of the running app (web/ui.html), one pair per subject —
     see site.css for why src is never in the markup. setSrc guards the
     assignment: writing src RESTARTS the request even when the value is
     unchanged, which would re-abort the same fetch every time the theme is
     toggled back and forth. */
  var shots = [].slice.call(document.querySelectorAll("img.themeshot"));
  function setSrc(el, path) {
    if (el && el.getAttribute("src") !== path) el.setAttribute("src", path);
  }
  function paintShots() {
    var t = root.dataset.theme === "light" ? "light" : "dark";
    shots.forEach(function (im) { setSrc(im, "./assets/app/" + im.dataset.shot + "-" + t + ".webp"); });
  }
  // Every img.themeshot ships `hidden` in the markup — start hidden, reveal
  // on success — rather than starting visible and hiding on error. A fetch
  // that 404s (the captures don't exist yet, see site.css) still takes a
  // beat to fail; starting visible left a window where the browser had
  // already painted its broken-image glyph and the raw alt text before the
  // error handler could catch up. Starting hidden means the placeholder
  // behind it is the only thing ever on screen until a real file loads.
  //
  // The cost of that trick: `hidden` is display:none, and a display:none
  // element has no layout box, so native lazy-loading can never decide it is
  // near the viewport. A themeshot marked loading="lazy" therefore never
  // fetches at all — its slot sits on the placeholder forever and the server
  // log shows zero requests for it. Every themeshot must stay loading="eager".
  shots.forEach(function (im) {
    im.addEventListener("error", function () { im.hidden = true; });
    im.addEventListener("load", function () { im.hidden = false; });
  });
  var ICON = {
    sun: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"><circle cx="12" cy="12" r="4.2"/><path d="M12 2.4v2M12 19.6v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M2.4 12h2M19.6 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4"/></svg>',
    moon: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20.8 13.2A8.6 8.6 0 1 1 10.8 3.2a6.7 6.7 0 0 0 10 10z"/></svg>',
  };
  setTheme(root.dataset.theme || "dark");
  document.querySelectorAll("[data-theme-toggle]").forEach(function (b) {
    b.addEventListener("click", function () {
      setTheme(root.dataset.theme === "dark" ? "light" : "dark");
    });
  });

  /* ── guilloché ────────────────────────────────────────────────────────
     A hypotrochoid — the curve a geometric lathe traces, and the construction
     behind the lathe-work on banknotes:

        x = (R − r)·cos t + d·cos(((R − r)/r)·t)
        y = (R − r)·sin t − d·sin(((R − r)/r)·t)

     Generated rather than shipped as a file: the path data for four rings at
     this resolution is ~120 KB of text, which is larger than every font on the
     page put together. It is decoration, so if scripting is off nothing here
     is missed. */
  function rosette(R, r, d, turns, steps) {
    var k = (R - r) / r, out = new Array(steps + 1), i, t;
    for (i = 0; i <= steps; i++) {
      t = (i / steps) * turns * Math.PI * 2;
      out[i] = ((R - r) * Math.cos(t) + d * Math.cos(k * t)).toFixed(2) + "," +
               ((R - r) * Math.sin(t) - d * Math.sin(k * t)).toFixed(2);
    }
    return "M" + out.join("L") + "Z";
  }

  var RINGS = [
    [200, 31, 96, 31, 1500, 0.7, 0.55],
    [200, 47, 74, 47, 1800, 0.55, 0.4],
    [152, 23, 66, 23, 1200, 0.6, 0.45],
    [118, 17, 52, 17, 900, 0.5, 0.32],
  ];

  var nodes = document.querySelectorAll(".guilloche");
  if (nodes.length) {
    var paths = RINGS.map(function (a) {
      return { d: rosette(a[0], a[1], a[2], a[3], a[4]), w: a[5], o: a[6] };
    });
    var draw = function (list, extra) {
      return list.map(function (p) {
        return '<path d="' + p.d + '" stroke-width="' + p.w + '" opacity="' + (p.o * (extra || 1)) +
               '" vector-effect="non-scaling-stroke"/>';
      }).join("");
    };
    nodes.forEach(function (el, i) {
      var rev = i % 2 ? " rev" : "";
      el.innerHTML =
        '<svg viewBox="-220 -220 440 440" fill="none" aria-hidden="true">' +
          '<g class="rose' + rev + '" stroke="currentColor">' + draw(paths) + "</g>" +
          '<g class="rose' + (rev ? "" : " rev") + '" stroke="currentColor" transform="scale(.62)">' +
            draw(paths.slice(0, 2), 0.7) +
          "</g>" +
        "</svg>";
    });
  }

  /* ── reveals ──────────────────────────────────────────────────────────── */
  var rv = document.querySelectorAll(".rv");
  if (rv.length) {
    if (!("IntersectionObserver" in window) ||
        window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      rv.forEach(function (el) { el.classList.add("in"); });
    } else {
      var io = new IntersectionObserver(function (entries) {
        entries.forEach(function (e) {
          if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); }
        });
      }, { threshold: 0.08, rootMargin: "0px 0px -6% 0px" });
      rv.forEach(function (el) { io.observe(el); });
    }
  }

  /* ── count-up numbers ─────────────────────────────────────────────────────
     Every .count element's own text content IS the target value AND the
     no-JS / reduced-motion fallback: nothing here changes what the number
     eventually settles on, only how it gets there. Reading the target back
     out of the DOM instead of a data-attribute means the two can never
     drift apart — there is only one number to keep correct. */
  var counts = document.querySelectorAll(".count");
  if (counts.length && "IntersectionObserver" in window &&
      !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    var animateCount = function (el) {
      var target = el.textContent.trim();
      var value = parseFloat(target);
      if (!isFinite(value)) return;
      var decimals = (target.split(".")[1] || "").length;
      var start = null, dur = 850;
      function tick(now) {
        if (start === null) start = now;
        var p = Math.min(1, (now - start) / dur);
        var eased = 1 - Math.pow(1 - p, 3);
        el.textContent = (value * eased).toFixed(decimals);
        if (p < 1) requestAnimationFrame(tick);
        else el.textContent = target; // exact original string, no float drift
      }
      requestAnimationFrame(tick);
    };
    var countIo = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) { animateCount(e.target); countIo.unobserve(e.target); }
      });
    }, { threshold: 0.4 });
    counts.forEach(function (el) { countIo.observe(el); });
  }

  /* ── syntax highlighting ─────────────────────────────────────────────────
     Vendored highlight.js (assets/vendor/highlight.min.js) tokenises every
     code block — the landing's terminal/response plates and the docs
     viewer's rendered markdown alike — so no token colour is ever
     hand-authored. Exposed as window.orHighlight because docs.html injects
     its markdown asynchronously, after this script has already run once; it
     re-invokes this against the freshly-rendered subtree.

     A `data-prompt` block additionally gets its literal "$ " line prefix
     split into its own <span class="prompt"> before the rest of the line is
     handed to hljs — the highlighter doesn't model shell prompts (none do),
     so this stays a structural split, not a hand-picked token colour. */
  function highlightRoot(root) {
    if (!window.hljs) return;
    (root || document).querySelectorAll("pre code[data-prompt]").forEach(function (el) {
      if (el.dataset.hlDone) return;
      var lang = (el.className.match(/language-(\w+)/) || [0, "bash"])[1];
      var src = el.textContent.replace(/\n$/, "").split("\n");
      el.innerHTML = src.map(function (line) {
        if (line.slice(0, 2) === "$ ") {
          return '<span class="prompt">$</span> ' + hljs.highlight(line.slice(2), { language: lang }).value;
        }
        return line === "" ? "" : hljs.highlight(line, { language: lang }).value;
      }).join("\n");
      el.classList.add("hljs");
      el.dataset.hlDone = "1";
    });
    (root || document).querySelectorAll('pre code[class*="language-"]:not([data-prompt])').forEach(function (el) {
      if (el.dataset.hlDone) return;
      hljs.highlightElement(el);
      el.dataset.hlDone = "1";
    });
  }
  highlightRoot(document);
  window.orHighlight = highlightRoot;
})();
