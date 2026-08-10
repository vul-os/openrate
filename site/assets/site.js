/* openrate — site behaviour. Shared by index.html and docs.html.
   No dependencies, no network calls. Theme switch, product screenshots,
   the guilloché engraving, scroll reveals, and syntax highlighting. */

(function () {
  "use strict";

  /* ── theme ────────────────────────────────────────────────────────────
     Keyed the same as the app ("or-theme"), so a reader who set the paper
     theme in the converter and then follows a link to the docs does not get
     flipped back to ink. The resolved theme is decided by the inline script in
     <head> — stored choice, else prefers-color-scheme, else the markup's
     dark — because this file is deferred and therefore runs after the first
     paint. This only wires the toggle.

     The toggle is two-state and always writes an explicit value: once a reader
     has clicked it, that choice is theirs and outranks the machine on every
     later visit, in both directions. So a reader on a light machine who clicks
     to ink and back ends on a STORED light, not on "follow the machine" — the
     same pixels either way, and the button never has to report a state it has
     no glyph for.

     The sun/moon swap is done in CSS off [data-theme] (see .tools in
     site.css), not here, so the glyph is right at first paint too.

     applyTheme does NOT write to storage, and that separation is the whole
     point: the old code called setTheme() once on every load, which stored
     whatever the page happened to be showing. A reader who had merely VISITED
     once was thereafter carrying an "explicit choice" they never made, and it
     would outrank their machine's setting forever. Storage is written on click
     and nowhere else. */
  var root = document.documentElement;
  function applyTheme(t) {
    // Guarded, so the run at load re-stamps nothing: data-theme is written
    // exactly once, by the head script, before the first paint. An
    // unconditional assignment of the same value still counts as an attribute
    // change, and "the theme attribute is never touched after <head>" is the
    // property that says there is no flash.
    if (root.dataset.theme !== t) root.dataset.theme = t;
    var m = document.querySelector('meta[name="theme-color"]');
    if (m) m.setAttribute("content", t === "dark" ? "#06090E" : "#F2EFE6");
    document.querySelectorAll("[data-theme-toggle]").forEach(function (b) {
      b.setAttribute("aria-label", t === "dark" ? "Switch to paper theme" : "Switch to ink theme");
    });
    paintShots();
  }
  function chooseTheme(t) {
    applyTheme(t);
    try {
      localStorage.setItem("or-theme", t);
      /* The marker is what makes the value a choice. "or-theme" alone was
         written on every load by both this site and the app at /ui, so an
         unmarked value is probably an accident — see the head script. */
      localStorage.setItem("or-theme-set", "1");
    } catch (e) { /* private mode */ }
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
  applyTheme(root.dataset.theme === "light" ? "light" : "dark");
  document.querySelectorAll("[data-theme-toggle]").forEach(function (b) {
    b.addEventListener("click", function () {
      chooseTheme(root.dataset.theme === "light" ? "dark" : "light");
    });
  });
  /* A reader who has not pinned a choice keeps following the machine, live —
     flipping the OS to dark while the page is open flips the page. Once they
     click, the stored value wins and this stops mattering. */
  if (window.matchMedia) {
    var mql = window.matchMedia("(prefers-color-scheme: light)");
    var onSystem = function () {
      var stored = null;
      try {
        if (localStorage.getItem("or-theme-set") === "1") stored = localStorage.getItem("or-theme");
      } catch (e) { /* private mode */ }
      /* An unmarked stored theme must not stop the page following the machine
         live, or the readers this repair is for would still be stuck. */
      if (stored === "light" || stored === "dark") return;
      applyTheme(mql.matches ? "light" : "dark");
    };
    if (mql.addEventListener) mql.addEventListener("change", onSystem);
    else if (mql.addListener) mql.addListener(onSystem);
  }

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
  /* ── one block box per source line ───────────────────────────────────────
     Re-wraps an already-highlighted <code> so that every source line becomes
     its own <span class="cl">. That is what buys the hanging indent in
     site.css: a soft-wrapped continuation is set 2.6ch in from where its line
     began, so a wrap is visibly a wrap and not a newline, and a JSON or Go
     block's leading indentation still reads as structure after it wraps.
     There is no CSS-only way to get there — a <pre> is a single block box, so
     text-indent would move the first line of the whole listing and nothing
     else.

     The split happens on the highlighted DOM rather than by highlighting line
     by line, which would have been three lines of code instead of thirty. The
     difference is state: a highlighter carries context across lines, and a Go
     raw-string literal or a bash heredoc spanning several lines is one token.
     Fed a line at a time it stops being one token, and the highlighting goes
     quietly wrong for exactly the constructs that are hardest to read. Today's
     corpus happens to contain none of them; the docs are generated from the
     canonical docs and the next edit is not this file's to predict.

     Ancestor spans are rebuilt inside each line (a token interrupted by a
     newline reopens on the next line), so the class structure hljs produced
     survives intact. */
  function relineCode(code) {
    var lines = [], cur = null;
    function newLine() {
      var el = document.createElement("span");
      el.className = "cl";
      cur = { el: el, chain: [] };
      lines.push(cur);
    }
    function put(ancestors, text) {
      var chain = cur.chain, i = 0;
      while (i < chain.length && i < ancestors.length && chain[i].src === ancestors[i]) i++;
      chain.length = i;
      for (var j = i; j < ancestors.length; j++) {
        var clone = ancestors[j].cloneNode(false);
        (chain.length ? chain[chain.length - 1].node : cur.el).appendChild(clone);
        chain.push({ src: ancestors[j], node: clone });
      }
      (chain.length ? chain[chain.length - 1].node : cur.el).appendChild(document.createTextNode(text));
    }
    newLine();
    (function walk(node, ancestors) {
      for (var i = 0; i < node.childNodes.length; i++) {
        var c = node.childNodes[i];
        if (c.nodeType === 3) {
          var parts = c.nodeValue.split("\n");
          for (var p = 0; p < parts.length; p++) {
            if (p > 0) newLine();
            if (parts[p] !== "") put(ancestors, parts[p]);
          }
        } else if (c.nodeType === 1) {
          walk(c, ancestors.concat([c]));
        }
      }
    })(code, []);
    // A listing normally ends with a newline, which leaves one empty line
    // behind it. Dropping it stops every block growing a blank last row.
    while (lines.length > 1 && lines[lines.length - 1].el.childNodes.length === 0) lines.pop();
    code.textContent = "";
    for (var k = 0; k < lines.length; k++) {
      // The hanging indent has to hang from the line's OWN indentation, not
      // from the block's left edge. A flat 2.6ch would put the continuation of
      // a four-space-indented JSON line further LEFT than the line it belongs
      // to, which reads as a step back out to the outer level — the opposite
      // of what a wrap means. Publishing each line's leading whitespace lets
      // site.css offset from it, so a continuation always sits inside its own
      // line and a wrap can never be mistaken for structure.
      var lead = (lines[k].el.textContent.match(/^[ \t]*/) || [""])[0];
      var cols = lead.replace(/\t/g, "    ").length;   // tab-size: 4, set in site.css
      if (cols) lines[k].el.style.setProperty("--in", cols + "ch");
      code.appendChild(lines[k].el);
    }
  }
  window.orReline = relineCode;

  /* ── the shell command line, which no highlighter models ──────────────────
     The docs' most-read page is the Run/install section of overview.md, and
     it was four code plates of flat grey. That was not a palette problem and
     not a missing grammar: bash IS registered in the vendored bundle. It is
     that hljs's bash grammar describes shell *syntax* — keywords, quoting,
     expansion, builtins — and an install line contains none of it. Measured,
     the four blocks on that page emitted exactly ONE token between them (the
     `# serves :8080` comment on the first line); `go run ./cmd/openrate`,
     `docker build -t openrate .` and the two curl/verify lines tokenised to
     nothing at all and painted as --code-text from end to end.

     So the two things a reader actually scans an install line for are the two
     things nothing was marking: WHAT is being run, and WHICH options it is
     being run with. Both are structural facts about a command line, so they
     are taught to the grammar rather than paint-by-numbers on the output:

       command position  the first word of a line or of a pipeline segment
                         (also after ; && || and inside $( ) — ./relative and
                         /absolute paths included) takes the function role,
                         which is where hljs already puts a call.
       option            a -x / --long-form argument takes `attr`, hljs's own
                         scope for an option name.

     Three guards, each for a case the naive version got wrong:

       • the grammar's own keyword/builtin/literal table is read back out of
         it and turned into a negative lookahead, so `if`, `then`, `fi`, `for`
         and `echo`/`export` still reach hljs and still highlight as what they
         are. Built from lang.keywords rather than a list copied here, so it
         cannot drift from whatever bundle is vendored.
       • a word followed by `=` is not a command — `OPENRATE_SOURCES=ecb,sarb
         go run …` is an environment prefix, and `?from=USD&to=ZAR` is a query
         string, neither of which is being invoked.
       • [|;&]+ rather than [|;&], or the second `&` of `&&` is the one that
         matches and `&& ./openrate` loses its command.

     Both modes are relevance 0: they must never change which language hljs
     auto-detects, only how a block already known to be shell is drawn. */
  function teachCommandLine(hljs) {
    if (!hljs || !hljs.getLanguage) return;
    ["bash", "sh", "shell"].forEach(function (name) {
      var lang = hljs.getLanguage(name);
      if (!lang || !Array.isArray(lang.contains) || lang.orCommandMode) return;
      var kw = lang.keywords || {};
      var own = [].concat(kw.keyword || [], kw.built_in || [], kw.literal || [])
        .map(String)
        .sort(function (a, b) { return b.length - a.length; })   // longest first, so `return` wins over `re`
        .map(function (w) { return w.replace(/[.*+?^${}()|[\]\\-]/g, "\\$&"); });
      var guard = own.length ? "(?!(?:" + own.join("|") + ")\\b)" : "";
      lang.contains.unshift(
        {
          scope: { 2: "title.function_" },
          match: [/(?:^|[|;&]+\s*|\$\(\s*)/,
                  new RegExp(guard + "(?![\\w.\\/+-]*=)(?:\\.{0,2}\\/)?[\\w][\\w.\\/+-]*")],
          relevance: 0,
        },
        {
          scope: { 2: "attr" },
          match: [/\s/, /--?[A-Za-z][\w-]*/],
          relevance: 0,
        }
      );
      lang.orCommandMode = true;
    });
  }
  teachCommandLine(window.hljs);

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
      relineCode(el);
    });
    (root || document).querySelectorAll('pre code[class*="language-"]:not([data-prompt])').forEach(function (el) {
      if (el.dataset.hlDone) return;
      hljs.highlightElement(el);
      el.dataset.hlDone = "1";
      relineCode(el);
    });
  }
  highlightRoot(document);
  window.orHighlight = highlightRoot;
})();
