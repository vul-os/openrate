package abi

import "github.com/vul-os/openrate"

// Version is the string openrate_abi_version() returns: the openrate release
// this library was built from, so a host that has loaded a stale .so from its
// library path can find out before it makes a call that means something
// different than it used to.
//
// It is a DERIVATION, not a declaration, and that is the whole point. This used
// to be `const Version = "0.1.6"` — a second copy of a string that also lives in
// /VERSION — and in v0.1.3 the two came apart: VERSION was bumped for the
// release and this was not, so the library told every host it was 0.1.2 and the
// staleness check reported "old" for a current build. Nothing caught it, because
// the test that would have lives in THIS module, and ffi/ is a separate module
// that the repository root's `go test ./...` cannot reach.
//
// Now there is nothing to keep in sync. openrate.Version is embedded from the
// VERSION file at compile time (see /version.go for why the embed has to live up
// there), and this module's go.mod `replace`s the library with the checkout one
// directory up — so the string compiled into the shared library is, by
// construction, the VERSION file of the tree it was built from. A shared library
// has no checkout to read at RUNTIME, but it does have one at BUILD time, and
// that is the moment the value is captured.
//
// The C header is the one copy that cannot be derived this way: the C
// preprocessor cannot read a file, so OPENRATE_ABI_VERSION has to be a literal
// in ffi/include/openrate.h. It is generated from this value rather than typed
// (`go generate ./...`, see /internal/abiheader), and four separate things fail
// if it is stale: scripts/check-ffi.sh refuses to build, the C smoke test
// strcmp's the header against the loaded library, and both modules' test suites
// assert that regenerating the header changes nothing.
var Version = openrate.Version
