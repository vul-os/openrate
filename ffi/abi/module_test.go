package abi

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTheInternalWallHoldsForTheABI proves the C ABI is built from openrate's
// public API and nothing else.
//
// This module is named github.com/vul-os/openrate-ffi, with a hyphen, so that
// it sits OUTSIDE github.com/vul-os/openrate/ and Go's internal/ rule applies
// to it exactly as it applies to a third-party embedder. Rename it to
// .../openrate/ffi and the rule silently stops applying — the internal/ check
// is on the import path, not the module boundary — and the shared library other
// languages load could quietly be built from a wider surface than any of them
// could reach themselves.
//
// internalprobe/ is an import across that wall, behind a build tag. This builds
// it and FAILS IF THE BUILD SUCCEEDS.
func TestTheInternalWallHoldsForTheABI(t *testing.T) {
	cmd := exec.Command("go", "build", "-tags", "internalprobe", "../internalprobe")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the ffi module was allowed to import github.com/vul-os/openrate/internal/redact.\n"+
			"The C ABI is not being built from the public API, and the module's name is no longer\n"+
			"doing the job it was chosen for. Check that ffi/go.mod's module path is OUTSIDE\n"+
			"github.com/vul-os/openrate/.\ngo build said: %s", out)
	}
	if !strings.Contains(string(out), "use of internal package") {
		t.Fatalf("the build failed, but not because of the internal/ rule — so this test is\n"+
			"measuring a compile error of some other kind and would keep passing after the wall\n"+
			"came down.\ngo build said: %s", out)
	}
	t.Logf("compiler refused as required: %s", strings.TrimSpace(string(out)))
}

// TestHandlesAreNeverReused pins the shared convention llmux and openrate both
// follow: a closed handle's number is retired, not recycled.
//
// It is what makes use-after-close a readable error instead of a silent
// success against somebody else's object. A registry that reused slots would
// hand a stale handle in one part of a host program access to an engine another
// part had just created, and nothing would report a problem.
func TestHandlesAreNeverReused(t *testing.T) {
	seen := map[uint64]bool{}
	for i := 0; i < 50; i++ {
		h := mustNew(t, `{"quiet":true}`)
		if seen[h] {
			t.Fatalf("handle %d was issued twice; the registry recycles closed handles, so a "+
				"use-after-close in a host program would silently reach a different object", h)
		}
		seen[h] = true
		// Close immediately, so a registry that reused the freed slot would
		// hand the same number back on the very next iteration.
		Close(h)
	}
	if len(seen) != 50 {
		t.Fatalf("only %d distinct handles across 50 create/close cycles", len(seen))
	}
}
