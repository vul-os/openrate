// This file exists to be REFUSED by the compiler.
//
// It is the executed half of G1. The rest of embedtest proves the public API
// is sufficient by compiling; this proves that the wall it is standing outside
// of is actually there, by importing across it and being turned away.
// TestG1InternalPackagesStayUnreachable builds this package and fails if the
// build SUCCEEDS.
//
// Without it, the whole embeddability suite could go green from inside the
// openrate module — which is exactly what happened in the first draft, where
// the module was named github.com/vul-os/openrate/embedtest and could import
// internal/ freely. See go.mod.
//
// The build tag keeps it out of `go build ./...`, `go vet ./...` and
// `go test ./...`; only the test that expects the failure asks for it.
//go:build internalprobe

package internalprobe

import (
	// The import the module boundary must refuse. redact is picked because it
	// is small, stable and has no reason ever to be promoted out of internal/.
	_ "github.com/vul-os/openrate/internal/redact"
)
