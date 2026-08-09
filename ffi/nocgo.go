//go:build !cgo

// This file is why `CGO_ENABLED=0 go build ./...` still works.
//
// The rest of this package is cgo, and cgo excludes its own files when cgo is
// off. Without something here, the package would have zero Go files in that
// configuration and the toolchain would stop with "build constraints exclude
// all Go files in ffi" — turning openrate's pure-Go, stdlib-only build (the
// Dockerfile builds with CGO_ENABLED=0) into a build that fails because of a
// package it never intended to use.
//
// So the package still exists without cgo; it is simply empty. Building the
// shared library requires cgo and a C toolchain, which is one of the honest
// costs listed in ffi/README.md.
package main

func main() {}
