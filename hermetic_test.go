package openrate_test

// The unit suite must not talk to anything but this machine.
//
// It used to. Every test of Start ran against the live ECB endpoint, which put
// a central bank in the path of `go test`, and — because a cancelled request
// does not take its plumbing with it — leaked net/http dial and DNS goroutines
// into whatever ran next, where they were twice miscounted as something a
// constructor had started.
//
// The obvious way to gate this does not work, and it is worth writing down why
// rather than discovering it again. Running the suite with no network at all
// (a container with --network none, which CI also does) proves the tests do not
// DEPEND on the network. It does not prove they do not ATTEMPT it: pointing a
// test back at the live endpoint and running it under --network none still
// passed, because the fetch failed, was logged, and the test never looked. The
// attempt is the part that costs — the third party, and the leaked goroutines.
//
// So this counts attempts instead of blocking them. safedial is the single
// chokepoint every feed client dials through, and it now keeps an atomic tally
// of dials to anywhere that is not loopback. Zero is the only correct value
// here; httptest servers are 127.0.0.1 literals and are not counted.
//
// Scope worth stating: this covers the openrate package's own tests, which is
// where the regression was. A package that acquires its own live call would
// need its own TestMain.

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
			"\nFAIL\tthe openrate package's tests made %d outbound dial(s) to a non-loopback address.\n"+
				"\tThis suite is meant to be hermetic: point the test at a double\n"+
				"\t(openrate.StubStartSources) or an httptest server instead of a real source.\n"+
				"\tA live call puts a third party in the path of `go test` and leaves its dial\n"+
				"\tgoroutines running into the next test.\n", made)
		code = 1
	}
	os.Exit(code)
}
