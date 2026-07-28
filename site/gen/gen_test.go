package main

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// minPages is the coverage floor for the whole guard. Every test below asserts
// against it: a generator that was quietly emptied out must fail, not pass by
// checking nothing.
const minPages = 9

// minLinks is the coverage floor for the link scan. The generated bundle
// currently carries far more than this; the floor exists so that a scan which
// silently stops matching links fails instead of reporting success.
const minLinks = 25

// repoRoot locates the module root, failing loudly rather than skipping — a
// docs guard that quietly does nothing is worse than no guard at all.
func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	r, err := findRoot(cwd)
	if err != nil {
		t.Fatalf("site/gen: %v — the docs guards verified NOTHING", err)
	}
	return r
}

func generated(t *testing.T, root string) map[string]string {
	t.Helper()
	files, err := generate(root)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(files) != len(pages) || len(pages) < minPages {
		t.Fatalf("generator produced %d files from %d pages (floor %d) — too few to be a real check",
			len(files), len(pages), minPages)
	}
	return files
}

// TestSiteDocsInSync is the anti-rot guard: site/docs must be exactly what the
// generator produces from the canonical docs. Edit docs/api.md and forget to
// regenerate, and this fails naming the file.
func TestSiteDocsInSync(t *testing.T) {
	root := repoRoot(t)
	want := generated(t, root)
	dir := filepath.Join(root, "site", "docs")

	for name, body := range want {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("site/docs/%s is MISSING: %v — run `go run ./site/gen`", name, err)
			continue
		}
		if string(got) != body {
			t.Errorf("site/docs/%s is STALE — it does not match what the canonical source generates. "+
				"The canonical doc is the single source of truth; run `go run ./site/gen` to regenerate.", name)
		}
	}

	// No orphans: a file in site/docs that the generator does not produce is a
	// hand-maintained copy, and hand-maintained copies are what rotted before.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read site/docs: %v", err)
	}
	seen := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		seen++
		if _, ok := want[e.Name()]; !ok {
			t.Errorf("site/docs/%s is generated from nothing — add its source to `pages` in "+
				"site/gen/main.go, or delete it; hand-maintained copies go stale", e.Name())
		}
	}
	if seen < minPages {
		t.Fatalf("only %d markdown files found in site/docs (floor %d) — the staleness scan verified almost nothing", seen, minPages)
	}
	t.Logf("site/docs: %d generated pages compared byte-for-byte against their canonical sources", seen)
}

// siteDocsOnDisk reads the markdown that site/docs actually ships. The link
// scan deliberately reads the FILES rather than the generator's output, so a
// hand-edited dead link is caught on its own merits and not only as drift.
func siteDocsOnDisk(t *testing.T, root string) map[string]string {
	t.Helper()
	dir := filepath.Join(root, "site", "docs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read site/docs: %v — the link scan verified NOTHING", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read site/docs/%s: %v", e.Name(), err)
		}
		out[e.Name()] = string(body)
	}
	if len(out) < minPages {
		t.Fatalf("only %d markdown files found in site/docs (floor %d) — the link scan verified almost nothing", len(out), minPages)
	}
	return out
}

// TestSiteDocsHaveNoDeadLinks checks the shipped bundle the way a visitor
// experiences it. site/docs.html injects the rendered markdown into itself, so
// every relative target resolves against site/ — it must be either a "#slug"
// route the nav can serve, or a file that actually ships in site/. An empty
// target (the four that shipped in site/docs/api.md) fails here too.
func TestSiteDocsHaveNoDeadLinks(t *testing.T) {
	root := repoRoot(t)
	files := siteDocsOnDisk(t, root)

	slugs := map[string]bool{}
	for _, p := range pages {
		slugs[p.slug] = true
	}

	checked := 0
	check := func(page, kind, target string) {
		if strings.TrimSpace(target) == "" {
			t.Errorf("DEAD LINK: site/docs/%s has an EMPTY %s target — it renders as an unclickable link", page, kind)
			checked++
			return
		}
		if isAbsolute(target) {
			return
		}
		checked++
		if strings.HasPrefix(target, "#") {
			if !slugs[strings.TrimPrefix(target, "#")] {
				t.Errorf("DEAD LINK: site/docs/%s %s %q is not a doc route — site/docs.html would fall back to the overview",
					page, kind, target)
			}
			return
		}
		if !shipsInSite(root, path.Clean(target)) {
			t.Errorf("DEAD LINK: site/docs/%s %s %q does not ship in the site bundle (site/%s is missing)",
				page, kind, target, target)
		}
	}

	for name, body := range files {
		inFence := false
		for _, line := range strings.Split(body, "\n") {
			if fenceRE.MatchString(line) {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			for _, m := range mdLinkRE.FindAllStringSubmatch(line, -1) {
				target := m[3]
				if i := strings.IndexAny(target, " \t"); i != -1 {
					target = target[:i]
				}
				check(name, "markdown link", target)
			}
			for _, m := range htmlAttrRE.FindAllStringSubmatch(line, -1) {
				if m[2] == "#" { // an unwrapped in-page anchor, deliberately inert
					continue
				}
				check(name, m[1], m[2])
			}
		}
	}

	if checked < minLinks {
		t.Fatalf("only %d relative links were checked (floor %d) — the link scan is broken and verified nothing", checked, minLinks)
	}
	t.Logf("site/docs: %d relative links checked across %d pages", checked, len(files))
}

// TestSiteDocsNavMatchesGenerator keeps site/docs.html's sidebar and the
// generator's page list from drifting apart: a nav entry for a page nobody
// generates is a "Could not load" error in the viewer, and a generated page
// missing from the nav is unreachable.
func TestSiteDocsNavMatchesGenerator(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "site", "docs.html"))
	if err != nil {
		t.Fatalf("read site/docs.html: %v", err)
	}
	html := string(raw)

	docsRE := regexp.MustCompile(`(?s)const DOCS = (\[.*?\]);`)
	m := docsRE.FindStringSubmatch(html)
	if m == nil {
		t.Fatal("could not find the `const DOCS = [...]` nav list in site/docs.html — the nav scan is broken and verified NOTHING")
	}
	var nav []struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(m[1]), &nav); err != nil {
		t.Fatalf("parse DOCS in site/docs.html: %v — the nav scan verified NOTHING", err)
	}
	if len(nav) < minPages {
		t.Fatalf("site/docs.html nav lists %d docs (floor %d) — the nav scan verified almost nothing", len(nav), minPages)
	}

	var navSlugs, genSlugs []string
	for _, n := range nav {
		navSlugs = append(navSlugs, n.Slug)
		// The clickable sidebar is separate markup from the DOCS array; both
		// must offer the same route or one of them 404s.
		if !strings.Contains(html, `data-slug="`+n.Slug+`"`) {
			t.Errorf("site/docs.html: DOCS lists %q but no sidebar link has data-slug=%q", n.Slug, n.Slug)
		}
	}
	for _, p := range pages {
		genSlugs = append(genSlugs, p.slug)
	}
	sort.Strings(navSlugs)
	sort.Strings(genSlugs)
	if strings.Join(navSlugs, ",") != strings.Join(genSlugs, ",") {
		t.Errorf("site/docs.html nav and the generator disagree:\n  nav:       %v\n  generated: %v", navSlugs, genSlugs)
	}
}

// canonicalDocs is every markdown doc in the repo that a reader is meant to
// read: the docs/ tree plus the root-level guides.
func canonicalDocs(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	entries, err := os.ReadDir(filepath.Join(root, "docs"))
	if err != nil {
		t.Fatalf("read docs/: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, "docs/"+e.Name())
		}
	}
	rootEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read repo root: %v", err)
	}
	for _, e := range rootEntries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	if len(out) < minPages {
		t.Fatalf("found only %d canonical markdown docs (floor %d) — the doc scan verified almost nothing", len(out), minPages)
	}
	return out
}

// TestEveryCanonicalDocIsAccountedFor makes the coverage decision explicit: a
// new guide must either be published to the site or be listed in notShipped
// with a reason. Without this, a new doc simply never reaches the site and
// nobody finds out.
func TestEveryCanonicalDocIsAccountedFor(t *testing.T) {
	root := repoRoot(t)
	docs := canonicalDocs(t, root)

	for _, d := range docs {
		if _, ok := slugFor(d); ok {
			continue
		}
		if reason, ok := notShipped[d]; ok {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s is in notShipped with an empty reason — say why it is not published", d)
			}
			continue
		}
		t.Errorf("%s is neither published to the site nor listed in notShipped — add it to `pages` in "+
			"site/gen/main.go, or to notShipped with a reason", d)
	}
	// The reverse: a notShipped entry for a file that no longer exists is a
	// stale exemption that could hide a real gap later.
	have := map[string]bool{}
	for _, d := range docs {
		have[d] = true
	}
	for d := range notShipped {
		if !have[d] {
			t.Errorf("notShipped lists %q, which does not exist — drop the stale exemption", d)
		}
	}
	t.Logf("canonical docs: %d accounted for (%d published, %d exempt)", len(docs), len(pages), len(notShipped))
}

// TestCanonicalDocLinksResolve catches the breakage at the source, before it can
// be generated into the site: every relative link in a canonical doc must point
// at a file that exists, and no link may have an empty target.
func TestCanonicalDocLinksResolve(t *testing.T) {
	root := repoRoot(t)
	docs := canonicalDocs(t, root)

	checked := 0
	for _, d := range docs {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(d)))
		if err != nil {
			t.Fatalf("read %s: %v", d, err)
		}
		dir := path.Dir(d)
		inFence := false
		for i, line := range strings.Split(string(body), "\n") {
			if fenceRE.MatchString(line) {
				inFence = !inFence
				continue
			}
			if inFence {
				continue
			}
			var targets []string
			for _, m := range mdLinkRE.FindAllStringSubmatch(line, -1) {
				target := m[3]
				if j := strings.IndexAny(target, " \t"); j != -1 {
					target = target[:j]
				}
				targets = append(targets, target)
			}
			for _, m := range htmlAttrRE.FindAllStringSubmatch(line, -1) {
				targets = append(targets, m[2])
			}
			for _, target := range targets {
				if strings.TrimSpace(target) == "" {
					t.Errorf("%s:%d: EMPTY link target — write a real target, or plain text with no link", d, i+1)
					checked++
					continue
				}
				if isAbsolute(target) || strings.HasPrefix(target, "#") {
					continue
				}
				checked++
				file := target
				if j := strings.IndexByte(file, '#'); j != -1 {
					file = file[:j]
				}
				if file == "" {
					continue
				}
				rel := path.Clean(path.Join(dir, file))
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
					t.Errorf("%s:%d: DEAD LINK %q — %s does not exist in the repo", d, i+1, target, rel)
				}
			}
		}
	}
	if checked < minLinks {
		t.Fatalf("only %d relative links were checked across the canonical docs (floor %d) — the scan verified nothing", checked, minLinks)
	}
	t.Logf("canonical docs: %d relative links resolved across %d files", checked, len(docs))
}
