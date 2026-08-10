package openrate

import (
	_ "embed"
	"strings"
)

// versionFile is the repository's VERSION file, embedded at COMPILE time.
//
// The embed has to happen in THIS directory. `//go:embed ../VERSION` is a
// compile error ("invalid pattern syntax" — embed patterns may not escape the
// package directory), and a symlink to it is rejected too ("cannot embed
// irregular file"). Both were tried; neither is a matter of taste. So the one
// package that can read VERSION at build time is the package that sits beside
// it, and everything that needs the version imports that package. embed is
// stdlib, so this does not cost the library its no-dependencies property.
//
//go:embed VERSION
var versionFile string

// Version is the openrate release this build came from — "0.1.6", no leading v.
//
// It is DERIVED, not declared. There is no copy of the version string in Go
// source to fall out of step with a release: bumping VERSION bumps this, and a
// build with no VERSION file next to this one does not compile at all.
//
// This is what openrate_abi_version() reports (see ffi/abi/version.go). That
// symbol exists so a host can compare the shared library it just dlopen'd
// against the version it was built for and refuse a stale .so on its load path.
// v0.1.3 shipped with VERSION at 0.1.3 and the ABI constant still reading
// 0.1.2, which made that check answer "stale" for a library that was current —
// the failure direction that costs a host its startup rather than merely its
// warning. A hand-typed constant is what made that possible, so there is no
// longer a hand-typed constant.
//
// The C header's OPENRATE_ABI_VERSION macro is a third copy that Go cannot
// derive, because the C preprocessor cannot read a file. It is GENERATED from
// this value instead:
//
//go:generate go run ./internal/abiheader/gen
var Version = strings.TrimSpace(versionFile)
