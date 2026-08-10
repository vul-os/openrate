package embedtest

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const mod = "github.com/vul-os/openrate"

// TestG3ImportDirection pins which way the arrows point.
//
// The refactor's whole claim is that the pure core is beneath everything else:
// fx knows nothing about sources, fxsource knows nothing about HTTP serving,
// and the console is a leaf. Nothing about that is enforced by the compiler
// except the ban on cycles, and a cycle is a long way past the point where the
// shape has rotted. One `go list -deps` per package is the cheapest guard on
// this list and the one most likely to fire.
//
// The root package is a deliberate exception and is asserted as such below.
func TestG3ImportDirection(t *testing.T) {
	// Exact sets, not forbidden lists, for the two packages whose entire value
	// is that they depend on nothing here. A new intra-repo dependency in fx is
	// news whatever it is, so the guard should not need to have anticipated it.
	exact := []struct {
		pkg  string
		want []string
		why  string
	}{
		{mod + "/fx", []string{mod + "/fx"},
			"fx is the pure core: a graph, a snapshot and an accuracy model. It must be " +
				"importable by a host that wants arithmetic and nothing else."},
		{mod + "/fxsource", []string{mod + "/fx", mod + "/fxsource", mod + "/internal/safedial"},
			"fxsource fetches and emits fx.Edge. It must never learn about the engine, " +
				"the server or the console. internal/safedial is allowed because it is " +
				"BENEATH fxsource, not beside it: it decides which addresses an outbound " +
				"dial may reach, knows nothing about rates, and is asserted below to " +
				"import nothing else in this repo. Sharing it is what stops the dial " +
				"policy from being copied into fxsource and internal/ratesources and " +
				"drifting between the two."},
		{mod + "/internal/safedial", []string{mod + "/internal/safedial"},
			"safedial is the bottom of the stack: a destination policy over net and " +
				"net/http. If it grows an intra-repo dependency, it is no longer beneath " +
				"the packages that import it and the allowance above stops being true."},
		{mod + "/serve/web", []string{mod + "/serve/web"},
			"serve/web is a leaf holding one embedded HTML file. Anything it imports is " +
				"something a -tags noui build cannot drop."},
	}
	for _, c := range exact {
		got := localDeps(t, c.pkg)
		if !sameSet(got, c.want) {
			t.Errorf("%s depends on %v within this repo, want exactly %v.\n%s", c.pkg, got, c.want, c.why)
		}
	}

	// serve may grow subpackages; what it may never do is depend on the engine.
	// (The compiler would catch the cycle today, because the root imports serve
	// — but that root->serve edge is scheduled for removal, and on the day it
	// goes this assertion is the only thing left holding the direction.)
	forServe := localDeps(t, mod+"/serve")
	for _, forbidden := range []string{mod, mod + "/serve/interest"} {
		if contains(forServe, forbidden) {
			t.Errorf("serve depends on %s. serve is a shell OVER an engine it is handed; "+
				"the moment it reaches back for one, an embedder gets the server whether "+
				"they wanted it or not.", forbidden)
		}
	}

	// CONTROL. The root package really does pull in serve and serve/web today
	// (via the deprecated Start), so this is the same lookup and the same
	// membership test as above, exercised on a case that must come back TRUE.
	// Without it, a `go list` that silently returned nothing would satisfy
	// every assertion in this test.
	forRoot := localDeps(t, mod)
	for _, want := range []string{mod + "/serve", mod + "/serve/web", mod + "/fx", mod + "/fxsource"} {
		if !contains(forRoot, want) {
			t.Fatalf("the root package's dependency list %v does not contain %s.\n"+
				"Either `go list -deps` is not returning real data — in which case every "+
				"assertion in this test passed by examining nothing — or the deprecated "+
				"Start shim has been removed, in which case delete its allowance in "+
				"TestG3RootDependsOnServeOnlyThroughTheDeprecatedShim.", forRoot, want)
		}
	}
}

// deprecatedShimFile is the single root-package file allowed to import the
// server: openrate.go, which holds the deprecated Start/Options/Local trio.
//
// Start is why `go list -deps .` includes serve and serve/web, and it is why
// the blanket "the library must not reach the server" assertion cannot be made
// at package level for the root today. It can be made at FILE level, which is
// what stops the shape rotting back in the meantime: any new root-package file
// that reaches for serve fails this test, and when Start is finally deleted
// the allowance goes with it and the check becomes absolute.
const deprecatedShimFile = "openrate.go"

func TestG3RootDependsOnServeOnlyThroughTheDeprecatedShim(t *testing.T) {
	root := repoRoot(t)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}

	fset := token.NewFileSet()
	parsed := 0
	shimImportsServe := false

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(root, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed++
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote import %s: %v", name, imp.Path.Value, err)
			}
			if !isServePkg(path) {
				continue
			}
			if name == deprecatedShimFile {
				shimImportsServe = true
				continue
			}
			t.Errorf("%s imports %s. Only %s (the deprecated Start shim) may reach the "+
				"server from the library's own package — everything else in the root is "+
				"the part an embedder gets whether they asked for a server or not.",
				name, path, deprecatedShimFile)
		}
	}

	// Coverage floor: a walk that found nothing to look at would report a clean
	// result. The root package has had at least four non-test files since the
	// refactor (openrate.go, engine.go, refresh.go, doc.go).
	if parsed < 4 {
		t.Fatalf("parsed only %d non-test .go file(s) in %s; this check is not looking at "+
			"the package it claims to be checking", parsed, root)
	}
	// And the allowance must still be earned. If Start is gone, so is the
	// exception — delete it rather than leaving a permanent hole named after a
	// file that no longer needs one.
	if !shimImportsServe {
		t.Fatalf("%s no longer imports the server. The deprecated Start shim is the only "+
			"reason the root package may depend on serve; if it has been removed, delete "+
			"deprecatedShimFile and make this check absolute.", deprecatedShimFile)
	}
}

func isServePkg(path string) bool {
	return path == mod+"/serve" || strings.HasPrefix(path, mod+"/serve/")
}

// localDeps returns the packages of this repo that pkg depends on, transitively
// and including itself, as `go list -deps` reports them.
func localDeps(t *testing.T, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s (in %s): %v\n%s", pkg, cmd.Dir, err, stderrOf(err))
	}
	var local []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == mod || strings.HasPrefix(line, mod+"/") {
			local = append(local, line)
		}
	}
	if len(local) == 0 {
		t.Fatalf("go list -deps %s reported no packages from this repo at all — not even "+
			"%s itself. The lookup is broken, and any 'does not depend on' assertion made "+
			"with it is worthless.", pkg, pkg)
	}
	sort.Strings(local)
	return local
}

func stderrOf(err error) string {
	if ee, ok := err.(*exec.ExitError); ok {
		return string(ee.Stderr)
	}
	return ""
}

// repoRoot is the openrate checkout: the directory holding the module this
// suite tests, which is one level up from this (separate) module.
func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "engine.go")); err != nil {
		t.Fatalf("%s does not look like the openrate checkout: %v", abs, err)
	}
	return abs
}
