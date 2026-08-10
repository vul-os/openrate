package fxsource

// These tests must not talk to anything but this machine.
//
// This package's whole job is building the things that dial, so it is the most
// likely place for a live call to reappear — a test that points a source at its
// real endpoint "just to check" looks completely ordinary in review and passes.
// The openrate package learned that the expensive way: every test of Start ran
// against the live ECB, which put a central bank in the path of `go test` and
// leaked the cancelled request's dial goroutines into whatever ran next.
//
// safedial is the single chokepoint every source client dials through and it
// counts dials to anywhere that is not loopback. Zero is the only correct value
// here; the httptest servers these tests use are 127.0.0.1 literals, which are
// not counted. Measured before this was added: the whole module already made
// zero, so this holds a property that is true rather than announcing an
// intention.

import (
	"fmt"
	"os"
	"testing"

	"github.com/vul-os/openrate/internal/safedial"
)

func TestMain(m *testing.M) {
	before := safedial.OffMachineDials()
	code := m.Run()
	made := safedial.OffMachineDials() - before

	if code == 0 && made > 0 {
		fmt.Fprintf(os.Stderr,
			"\nFAIL\tthe fxsource tests made %d outbound dial(s) to a non-loopback address.\n"+
				"\tPoint the source at an httptest server instead of its real endpoint: a live\n"+
				"\tcall makes this suite depend on a third party and leaves its dial goroutines\n"+
				"\trunning into the next test.\n", made)
		code = 1
	}
	os.Exit(code)
}
