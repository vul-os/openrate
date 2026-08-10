package openrate_test

// openrate_abi_version() reports the release the shared library was built from,
// so a host can spot a stale .so on its load path. That string used to be a
// hand-typed `const Version` in ffi/abi/version.go, and in v0.1.3 it came apart
// from /VERSION: the release was tagged and pushed with the constant a patch
// behind, so the library told every host it was old when it was current. llmux
// shipped the identical defect in the same release, from the same cause. Every
// check run before tagging was green, because ffi/ is a SEPARATE Go module and
// `go test ./...` here cannot descend into it — the pin that would have caught
// the drift lived inside the module that was wrong.
//
// The fix is structural: ffi/abi/version.go no longer declares a version, it
// derives one from openrate.Version, which is `go:embed`ed from VERSION at
// compile time. The C header's macro cannot join that chain (the preprocessor
// cannot read a file), so it is generated from the same value instead.
//
// These tests are the belt to that braces. They live in the ROOT module on
// purpose and read ffi/'s source as TEXT rather than importing it, because
// importing across the module boundary is exactly what is not possible — and so
// that the day someone types a literal back into ffi/abi/version.go, or edits
// the generated header line by hand, the root module's own `go test ./...` says
// so, without anyone having to remember that ffi/ has a suite of its own.
//
// The Go scan parses the file rather than grepping it, so that a version-shaped
// string in a COMMENT — including the ones above and in ffi/abi/version.go,
// which have to be able to quote the defect they describe — is not mistaken for
// a declaration.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/internal/abiheader"
)

// TestVersionIsTheVERSIONFile checks the embed points at what we think it does.
// Everything else derives from openrate.Version; if the embed were aimed at the
// wrong file, every other assertion here would agree with each other and be
// wrong together.
func TestVersionIsTheVERSIONFile(t *testing.T) {
	raw, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	want := strings.TrimSpace(string(raw))
	if want == "" {
		t.Fatal("VERSION is empty, so this test would compare nothing against nothing")
	}
	if openrate.Version != want {
		t.Errorf("openrate.Version is %q but the VERSION file on disk says %q", openrate.Version, want)
	}
}

// semverLiteral is a string that looks like a release number.
var semverLiteral = regexp.MustCompile(`^v?\d+\.\d+\.\d+`)

// TestFFIDerivesItsABIVersionRatherThanDeclaringOne is the guard that replaced
// the old string comparison. Comparing two copies of a version can only detect
// drift after someone has written it; this asserts there is no second copy that
// COULD drift, which is the property that actually holds now.
func TestFFIDerivesItsABIVersionRatherThanDeclaringOne(t *testing.T) {
	const path = "ffi/abi/version.go"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	// 1. The declaration is a derivation from the library's own version.
	var found ast.Expr
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || (gd.Tok != token.VAR && gd.Tok != token.CONST) {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name == "Version" && i < len(vs.Values) {
					found = vs.Values[i]
				}
			}
		}
	}
	if found == nil {
		t.Fatalf("%s declares no Version. openrate_abi_version() has nothing to report, or this "+
			"scan is looking in the wrong place — either way, fix it rather than deleting the test.", path)
	}
	sel, ok := found.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Version" {
		t.Errorf("%s initialises Version from %T, not from openrate.Version.\n"+
			"openrate_abi_version() must report the VERSION file of the tree the library was "+
			"built from, and the only way to guarantee that is to derive it. If this was "+
			"refactored rather than reverted, teach this test the new shape — do not replace "+
			"the derivation with a literal.", path, found)
	} else if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "openrate" {
		t.Errorf("%s initialises Version from %s.Version, not openrate.Version", path, sel.X)
	}

	// 2. No version-shaped literal survives anywhere in the file, whether or not
	//    the derivation above is still standing next to it.
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil || !semverLiteral.MatchString(s) {
			return true
		}
		t.Errorf("%s:%d contains the version-shaped literal %q.\n"+
			"That is the exact shape of the v0.1.3 defect: a release bumps VERSION, this does "+
			"not, and the shared library misreports itself to every host that asks. Derive it "+
			"from openrate.Version.", path, fset.Position(lit.Pos()).Line, s)
		return true
	})
}

// TestTheGeneratedHeaderIsUpToDate is the header's half. OPENRATE_ABI_VERSION
// is the one copy that cannot be derived — a C consumer needs a literal to
// compare the runtime string against, and the preprocessor cannot read
// /VERSION — so it is generated from openrate.Version and this asserts that
// regenerating it would change nothing.
//
// That is a stronger statement than "the macro equals VERSION": it also fails
// on a second #define, a macro that was renamed out from under the generator,
// or a hand-edit that left the line in a shape the generator would rewrite. Any
// of those would leave a consumer comparing against something other than what
// `go generate` produces, which is the value the release is built from.
func TestTheGeneratedHeaderIsUpToDate(t *testing.T) {
	path := filepath.Join("ffi", "include", "openrate.h")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if _, ok, err := abiheader.Check(src, openrate.Version); err != nil {
		t.Fatalf("%s: %v", path, err)
	} else if !ok {
		t.Errorf("%s is stale: `go generate ./...` would rewrite it.\n"+
			"openrate_abi_version() reports %q; a consumer that compares it against "+
			"%s would conclude it had loaded the wrong library. Run `go generate ./...` "+
			"and commit the result.", path, openrate.Version, abiheader.Macro)
	}
}
