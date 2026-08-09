// Command withui is the positive control for ../libonly: a host that imports
// serve and builds a handler, so the embedded page MUST end up in its binary.
//
// Without it, scripts/check-embed-linkage.sh would be a grep for a string in a
// file, with no evidence the grep can find that string in a Go binary at all. A
// typo in the sentinel, a compression change, a different embed mechanism: any
// of those would make "the UI is absent" true of every binary ever built,
// including the ones full of UI. This program is the case that must come back
// POSITIVE, checked by the same grep, in the same run.
//
// UI: true is what a console-serving host writes, but it is not what puts the
// bytes in the binary — serve.Handler references web.Handler from a branch, so
// the linker keeps the page whichever way the flag is set. That was measured,
// not assumed: flipping this to UI: false leaves the sentinel present. The line
// this guard draws is therefore "imports serve" versus "imports only the
// library", which is exactly the line an embedder chooses.
package main

import (
	"fmt"
	"net/http"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/serve"
)

func main() {
	e := openrate.NewEngine(openrate.EngineOptions{Base: "ZAR"})
	api := serve.New(e, serve.Options{UI: true})
	defer func() { _ = api.Close() }()

	var h http.Handler = api.Handler()
	fmt.Println("handler ready:", h != nil)
}
