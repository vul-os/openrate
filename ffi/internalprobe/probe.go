// This file exists to be REFUSED by the compiler.
//
// The C ABI is the surface every non-Go language gets, and the claim behind
// this module's name is that it is built from openrate's PUBLIC API and nothing
// else — the same surface a third-party embedder has, no shortcuts through
// internal/. That claim rests entirely on the module being named
// github.com/vul-os/openrate-ffi rather than github.com/vul-os/openrate/ffi,
// because Go applies the internal/ rule to the import PATH and not to the
// module boundary. A one-character difference between a real wall and no wall
// is not something to leave to a code review.
//
// TestTheInternalWallHoldsForTheABI builds this package and fails if the build
// SUCCEEDS. embedtest/internalprobe is the same guard for the same reason, and
// its go.mod records the day the slash-named version compiled cleanly and the
// whole suite went green over nothing.
//
// The build tag keeps it out of `go build ./...`, `go vet ./...` and
// `go test ./...`; only the test that expects the failure asks for it.
//go:build internalprobe

package internalprobe

import (
	// The import the module boundary must refuse. redact is small, stable, and
	// has no reason ever to be promoted out of internal/.
	_ "github.com/vul-os/openrate/internal/redact"
)
