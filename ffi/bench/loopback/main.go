// Command loopback is the sidecar side of the FFI benchmark: openrate's real
// HTTP API, over a real loopback listener, answering from a snapshot loaded
// from a file rather than from the network.
//
// It exists so the comparison is honest. "In-process is faster than HTTP" is
// the kind of claim that gets asserted rather than measured, and when it is
// measured it is usually measured against a straw man. This is the actual
// serve.Server the binary runs, over the actual snapshot the C benchmark loads
// into the shared library, answering the actual conversion the C benchmark
// asks for — so the only difference between the two numbers is the transport.
//
// It prints its base URL on stdout and then serves until it is killed.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/serve"
)

type snapshotFile struct {
	BuiltAt time.Time `json:"built_at"`
	Edges   []fx.Edge `json:"edges"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: %s ffi/bench/snapshot.json\n", os.Args[0])
		os.Exit(2)
	}

	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatalf("loopback: read snapshot: %v", err)
	}
	var sf snapshotFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		log.Fatalf("loopback: parse snapshot: %v", err)
	}
	if len(sf.Edges) == 0 {
		log.Fatal("loopback: the snapshot has no edges; the server would answer nothing")
	}

	// Grouped by source, exactly as ffi/abi's "load" method does it, so both
	// sides of the benchmark hold the same graph.
	g := fx.NewGraph()
	bySource := map[string][]fx.Edge{}
	var order []string
	for _, e := range sf.Edges {
		if _, seen := bySource[e.Source]; !seen {
			order = append(order, e.Source)
		}
		bySource[e.Source] = append(bySource[e.Source], e)
	}
	for _, name := range order {
		g.Replace(name, bySource[name])
	}

	// An Engine and a Server. No Refresher: this process must not fetch, or the
	// benchmark would be timing a moving snapshot.
	engine := openrate.NewEngine(openrate.EngineOptions{Base: "ZAR"})
	engine.Load(g.Materialize(sf.BuiltAt))

	api := serve.New(engine, serve.Options{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("loopback: listen: %v", err)
	}

	// The benchmark harness reads this line to find the port.
	fmt.Printf("http://%s\n", ln.Addr().String())
	_ = os.Stdout.Sync()

	srv := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.Serve(ln); err != nil {
		log.Fatalf("loopback: serve: %v", err)
	}
}
