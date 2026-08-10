// Package abiheader owns the one copy of the ABI version that Go cannot derive:
// the OPENRATE_ABI_VERSION macro in ffi/include/openrate.h.
//
// Everywhere else the version is a derivation. openrate.Version is embedded from
// /VERSION at compile time, and ffi/abi.Version is openrate.Version, so the
// string the shared library reports cannot disagree with the release it was
// built from — there is no second value to bump.
//
// The header cannot join that chain. It is consumed by a C compiler, and the C
// preprocessor has no way to read a file at compile time (#include takes C
// source, not "0.1.6\n"). A consumer needs the macro to exist as a literal so it
// can be compared against the runtime string:
//
//	if (strcmp(openrate_abi_version(), OPENRATE_ABI_VERSION) != 0) { ... }
//
// So one literal is unavoidable. What is avoidable is a HUMAN typing it. This
// package rewrites that line from openrate.Version, `go generate ./...` runs it,
// and Check reports whether the file on disk is what generation would produce —
// which is what the tests and scripts/check-ffi.sh assert, so a stale header
// fails a release rather than shipping in one.
package abiheader

import (
	"bytes"
	"fmt"
	"regexp"
)

// Macro is the header macro this package owns.
const Macro = "OPENRATE_ABI_VERSION"

// macroLine matches the whole #define line, so a rewrite replaces it rather than
// appending a second definition. It is anchored to the start of a line: a
// mention of the macro in the surrounding prose (there are several, explaining
// how to use it) must not be mistaken for the definition.
var macroLine = regexp.MustCompile(`(?m)^#define\s+` + Macro + `\s+"[^"]*"[^\n]*$`)

// Render returns the contents ffi/include/openrate.h should have for the given
// version.
//
// It edits rather than templates on purpose. The header is a hand-written,
// documented, stable interface — the comments around each entry point are the
// product, and regenerating the whole file from a template would put them under
// a generator nobody wants to edit prose in. Only the single #define line is
// machine-owned.
//
// A header with no #define is an ERROR, not something to fix by appending one.
// If the macro has been renamed or moved, generating a fresh one beside the old
// one would leave a C consumer comparing against whichever the preprocessor saw
// first; a human should look.
func Render(header []byte, version string) ([]byte, error) {
	if version == "" {
		return nil, fmt.Errorf("abiheader: refusing to write an empty %s; "+
			"a consumer's staleness check would compare against \"\" and always disagree", Macro)
	}
	if bytes.ContainsAny([]byte(version), "\"\\\n") {
		return nil, fmt.Errorf("abiheader: version %q contains a character that cannot go in a C "+
			"string literal unescaped", version)
	}
	found := macroLine.FindAll(header, -1)
	switch len(found) {
	case 1:
	case 0:
		return nil, fmt.Errorf("abiheader: the header defines no %s. A consumer has nothing to "+
			"compare openrate_abi_version()'s return value against, so the staleness check the "+
			"header documents cannot be written", Macro)
	default:
		return nil, fmt.Errorf("abiheader: the header defines %s %d times; a C consumer would "+
			"compare against whichever the preprocessor reached first", Macro, len(found))
	}
	return macroLine.ReplaceAll(header, []byte(fmt.Sprintf("#define %s %q", Macro, version))), nil
}

// Check reports whether the header on disk already is what Render would produce.
// It returns the rendered bytes either way so a caller can write them.
func Check(header []byte, version string) (want []byte, ok bool, err error) {
	want, err = Render(header, version)
	if err != nil {
		return nil, false, err
	}
	return want, bytes.Equal(want, header), nil
}
