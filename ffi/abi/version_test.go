package abi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A shared library has no checkout to read a VERSION file from at RUNTIME, so
// the version openrate_abi_version() reports is compiled in. It used to be
// compiled in three times — a Go constant here, the VERSION file, and the C
// header's macro — and in v0.1.3 the release bumped one of the three, so a host
// comparing 0.1.3 against 0.1.2 concluded its library was stale when it was not.
//
// Two of those three are now one. The build has a checkout even though the
// library does not: /version.go embeds VERSION at compile time and Version here
// is that value, reached through this module's `replace` directive. The header's
// macro cannot join in (the C preprocessor cannot read a file) and is generated
// from the same value by `go generate ./...` in the root module.
//
// These tests are what proves the chain actually connects. They cannot import
// the generator — internal/abiheader sits behind Go's internal wall, and this
// module is deliberately OUTSIDE the library's import-path prefix so that it is
// held to the same surface a third-party embedder has — so the header check
// below is an independent re-reading of the file, not a call into the thing
// that wrote it.

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
			"Version is supposed to BE openrate.Version, i.e. this very file's contents. If they "+
			"disagree, one of the three links is broken — the //go:embed in /version.go, the "+
			"TrimSpace, or the `replace github.com/vul-os/openrate => ../` in this module's "+
			"go.mod, which is what makes the library build against the working tree rather than "+
			"the last published tag.", Version, want)
	}
}

// abiVersionMacro pulls OPENRATE_ABI_VERSION out of the header. This is a
// second, independent reading of the line internal/abiheader generates — this
// module cannot import that package (see the note at the top of the file), and
// a check that called the generator to validate the generator's own output
// would agree with itself no matter what either of them did.
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
