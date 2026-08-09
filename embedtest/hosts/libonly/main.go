// Command libonly is a host program that imports openrate the way an embedder
// is supposed to: the library, and nothing else. It exists to be LINKED and
// then grepped — scripts/check-embed-linkage.sh builds it and asserts the
// resulting binary contains none of the console's bytes.
//
// That is not a given. The root openrate package still imports serve for the
// deprecated Start, and serve imports serve/web, so the embedded ui.html is
// reachable through the import graph of this very program. What keeps it out
// of the binary is the linker: nothing here can reach web.UI, so the data is
// dropped. This program plus its counterpart in ../withui is how that claim
// gets measured instead of asserted.
//
// It must never import serve. If it does, the guard still passes — and stops
// meaning anything.
package main

import (
	"fmt"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
)

func main() {
	now := time.Now().UTC()

	g := fx.NewGraph()
	g.Replace("libonly", []fx.Edge{
		{From: "USD", To: "ZAR", Rate: 18.5, Source: "libonly", Time: now},
	})

	e := openrate.NewEngine(openrate.EngineOptions{Base: "ZAR"})
	e.Load(g.Materialize(now))

	c, err := e.Convert("USD", "ZAR", 100)
	if err != nil {
		fmt.Println("convert:", err)
		return
	}
	fmt.Printf("100 USD = %.2f ZAR (%s, grade %s)\n", c.Result, c.Sources, c.Quality.Grade)
}
