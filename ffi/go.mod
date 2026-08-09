// ffi is a SEPARATE Go module on purpose, and its NAME is load-bearing.
//
// THE MODULE PATH MUST STAY OUTSIDE github.com/vul-os/openrate/.
// Go applies the internal/ rule to the import PATH, not to the module
// boundary. A module named github.com/vul-os/openrate/ffi would sit inside the
// tree rooted at openrate's internal/ parent and could import
// github.com/vul-os/openrate/internal/... freely — so the C ABI, the artifact
// every other language loads, would be built from a wider surface than any
// real embedder has. The hyphen is what stops that: openrate-ffi is outside the
// prefix, the compiler refuses internal/, and the shared library is provably
// built from the public API and nothing else. embedtest/go.mod carries the same
// warning for the same reason, and records the day the slash version compiled.
//
// Being a separate module also keeps the C ABI out of the pure-Go build
// entirely. openrate is stdlib-only with no require block; `go build ./...`,
// `go vet ./...` and `go test ./...` at the repository root do not descend into
// a nested module, so nothing here can add a dependency, a cgo requirement or a
// build-tag hazard to the library everyone else consumes. (A build tag would
// NOT have achieved that: a package whose every file is excluded by a tag is a
// hard error for `go build ./...`, not a skip.)
//
// The trade is that `go test ./...` at the root does not run these tests
// either, which is exactly how a suite stops executing without anyone noticing.
// CI therefore runs them through scripts/go-test-gate.sh with a floor and a
// list of required test names, the same way it runs embedtest.
//
// The replace below points at the checkout one directory up so the ABI is
// always built against the working tree rather than the last published tag —
// otherwise a change to the library and a change to its C ABI could not be
// tested in the same commit.
//
// No require line other than openrate itself may ever appear here. openrate is
// stdlib-only; a third-party dependency in its C ABI would be a dependency in
// every language binding that loads it.
module github.com/vul-os/openrate-ffi

go 1.25

toolchain go1.25.12

require github.com/vul-os/openrate v0.0.0

replace github.com/vul-os/openrate => ../
