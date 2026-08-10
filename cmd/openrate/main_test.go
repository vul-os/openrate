package main

import (
	"testing"

	"github.com/vul-os/openrate/serve/web"
)

// TestUIReportedMatchesWhatIsCompiledIn pins the startup line to reality.
//
// It reported the -ui FLAG, which is what the operator asked for rather than
// what they got: a binary built with -tags noui logged `ui=true` and then
// answered / with a 194-byte JSON stub. The console was not merely unmounted,
// it was not in the file — and the one line anybody reads to confirm the
// console is up said it was up.
//
// This test is written to run in both tag states, because CI runs the suite
// tagged and untagged: web.Embedded is the compile-time truth in each, so
// asserting against it rather than against a hard-coded true/false means one
// test covers both builds and neither can drift.
func TestUIReportedMatchesWhatIsCompiledIn(t *testing.T) {
	if got := uiServed(true); got != web.Embedded {
		t.Errorf("with -ui=true the startup line reports ui=%v, but web.Embedded is %v — "+
			"this build would advertise a console it does not have (or hide one it does)",
			got, web.Embedded)
	}
	if uiServed(false) {
		t.Error("with -ui=false the startup line reports ui=true; the console is not mounted, " +
			"so saying so is wrong in every build")
	}
}
