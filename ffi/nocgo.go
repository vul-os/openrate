//go:build !cgo

// This file is why `CGO_ENABLED=0 go build ./...` still works in this module.
//
// The rest of this package is cgo, and cgo excludes its own files when cgo is
// off. Without something here, the package would have zero Go files in that
// configuration and the toolchain would stop with "build constraints exclude
// all Go files" — a hard error, not a skip. That is worth knowing generally: a
// build tag is NOT a way to keep a package out of a build, because a package
// whose every file is excluded fails rather than disappearing. Keeping the C
// ABI out of openrate's pure-Go build is done by making this a separate module
// (see go.mod), which `./...` genuinely does not descend into.
//
// So the package still exists without cgo; it is simply empty. Building the
// shared library requires cgo and a C toolchain, which is one of the honest
// costs listed in README.md.
package main

func main() {}
