package openrate

// export_test.go is compiled into the test binary and nowhere else. Anything
// declared here is visible to the openrate_test package without becoming part
// of the module's public API — which is the point: Start is deprecated and
// should not grow exported surface, but it still needs to be testable without
// reaching the open internet.

import (
	"testing"

	"github.com/vul-os/openrate/fxsource"
)

// StubStartSources makes Start build exactly the given sources instead of
// resolving opts.Sources through the registry, and restores the real builder
// when the test ends.
//
// Before this existed, every test of Start ran against the live ECB endpoint.
// Two things came of that and both cost a red CI: the suite could not be
// trusted offline or when ecb.europa.eu was slow, and a cancelled request left
// net/http finishing its dial and parking a readLoop, which a neighbouring test
// counted as a goroutine its own constructors had started.
//
// Callers must not use t.Parallel: this swaps a package-level variable, and two
// tests doing that at once would each see the other's sources.
func StubStartSources(t *testing.T, ss ...fxsource.Source) {
	t.Helper()
	prev := buildSources
	buildSources = func(string) []fxsource.Source { return ss }
	t.Cleanup(func() { buildSources = prev })
}
