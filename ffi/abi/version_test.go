package abi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A shared library has no checkout to read a VERSION file from, so the version
// openrate_abi_version() reports is compiled in — in three places, because the
// C header has to carry a compile-time copy too or a consumer has nothing to
// compare the runtime value against.
//
// Three copies of a string drift. These tests are what stops a release bumping
// one and forgetting the others, which would leave a host comparing 0.1.3
// against 0.1.2 and concluding its library was stale when it was not — or, far
// worse, comparing 0.1.2 against 0.1.2 while the library was genuinely old.

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "engine.go")); err != nil {
		t.Fatalf("%s does not look like the openrate checkout: %v", root, err)
	}
	return root
}

func TestVersionMatchesTheVERSIONFile(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	want := strings.TrimSpace(string(b))
	if want == "" {
		t.Fatal("VERSION is empty, so this comparison would accept anything")
	}
	if Version != want {
		t.Fatalf("ffi/abi.Version is %q but VERSION says %q.\n"+
			"openrate_abi_version() exists so a host can detect a stale library on its load "+
			"path; a version that does not track the release makes that detection wrong in "+
			"both directions.", Version, want)
	}
}

// abiVersionMacro pulls OPENRATE_ABI_VERSION out of the hand-written header.
var abiVersionMacro = regexp.MustCompile(`(?m)^#define\s+OPENRATE_ABI_VERSION\s+"([^"]*)"`)

func TestHeaderDeclaresTheSameVersion(t *testing.T) {
	path := filepath.Join(repoRoot(t), "ffi", "include", "openrate.h")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	m := abiVersionMacro.FindSubmatch(b)
	if m == nil {
		t.Fatalf("%s does not define OPENRATE_ABI_VERSION. A consumer has nothing to compare "+
			"openrate_abi_version()'s return value against, so the staleness check the header "+
			"documents cannot be written.", path)
	}
	if got := string(m[1]); got != Version {
		t.Fatalf("the header says OPENRATE_ABI_VERSION is %q, the library reports %q", got, Version)
	}
}

// TestHeaderDeclaresEveryExportedSymbol is a coverage floor on the hand-written
// header. The generated header is always complete; this one is typed by hand,
// so an entry point added to the cgo layer and forgotten here would exist in the
// .so and be invisible to everyone binding against the documented interface.
func TestHeaderDeclaresEveryExportedSymbol(t *testing.T) {
	root := repoRoot(t)

	src, err := os.ReadFile(filepath.Join(root, "ffi", "openrate_ffi.go"))
	if err != nil {
		t.Fatalf("read the cgo layer: %v", err)
	}
	exported := regexp.MustCompile(`(?m)^//export\s+(\w+)`).FindAllSubmatch(src, -1)
	if len(exported) < 6 {
		t.Fatalf("found %d //export directives in the cgo layer; the ABI has at least six "+
			"entry points, so this scan is not reading what it thinks it is", len(exported))
	}

	hdr, err := os.ReadFile(filepath.Join(root, "ffi", "include", "openrate.h"))
	if err != nil {
		t.Fatalf("read the header: %v", err)
	}
	// A declaration, not a mention in prose: the symbol followed by an open
	// paren. Otherwise a name that only appears in a comment would satisfy this.
	for _, m := range exported {
		name := string(m[1])
		decl := regexp.MustCompile(`(?m)^\w[\w \*]*\b` + regexp.QuoteMeta(name) + `\s*\(`)
		if !decl.Match(hdr) {
			t.Errorf("the shared library exports %s and ffi/include/openrate.h does not declare it. "+
				"Everybody binding against the hand-written header cannot reach it.", name)
		}
	}
}
