/* openrate — site behaviour. Shared by index.html and docs.html.
   No dependencies, no network calls. Three things: the theme switch, the
   guilloché engraving, and scroll reveals. */

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
  }
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
  if (!rv.length) return;
  if (!("IntersectionObserver" in window) ||
      window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    rv.forEach(function (el) { el.classList.add("in"); });
    return;
  }
  var io = new IntersectionObserver(function (entries) {
    entries.forEach(function (e) {
      if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); }
    });
  }, { threshold: 0.08, rootMargin: "0px 0px -6% 0px" });
  rv.forEach(function (el) { io.observe(el); });
})();
