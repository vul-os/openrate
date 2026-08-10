// Command gen writes the OPENRATE_ABI_VERSION macro in ffi/include/openrate.h
// from openrate.Version, which is itself embedded from /VERSION.
//
//	go generate ./...                          rewrite the header if it is stale
//	go run ./internal/abiheader/gen -check     fail if it is stale, change nothing
//
// -check is the form CI and scripts/check-ffi.sh use: a generated file is only
// worth generating if something refuses to proceed when the checkout disagrees
// with what generation would produce.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/internal/abiheader"
)

func main() {
	check := flag.Bool("check", false, "do not write; exit non-zero if the header is stale")
	root := flag.String("root", ".", "path to the repository root")
	flag.Parse()

	if err := run(*root, *check); err != nil {
		fmt.Fprintf(os.Stderr, "gen-abi-header: %v\n", err)
		os.Exit(1)
	}
}

func run(root string, check bool) error {
	// Fail loudly on the wrong directory rather than quietly writing a header
	// somewhere unexpected, or reporting "up to date" about a file that is not
	// the one that ships.
	if _, err := os.Stat(filepath.Join(root, "VERSION")); err != nil {
		return fmt.Errorf("%s does not look like the openrate checkout (no VERSION): %w", root, err)
	}
	path := filepath.Join(root, "ffi", "include", "openrate.h")
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	want, ok, err := abiheader.Check(src, openrate.Version)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if ok {
		if !check {
			fmt.Printf("gen-abi-header: %s already declares %s %q\n",
				filepath.Join("ffi", "include", "openrate.h"), abiheader.Macro, openrate.Version)
		}
		return nil
	}
	if check {
		return fmt.Errorf("%s is stale: %s does not say %q.\n"+
			"  openrate_abi_version() reports %q, derived from the VERSION file, and a C consumer "+
			"comparing the two would conclude its library was the wrong one.\n"+
			"  Run `go generate ./...` and commit the result.",
			path, abiheader.Macro, openrate.Version, openrate.Version)
	}
	if err := os.WriteFile(path, want, 0o644); err != nil {
		return err
	}
	fmt.Printf("gen-abi-header: wrote %s %q to %s\n",
		abiheader.Macro, openrate.Version, filepath.Join("ffi", "include", "openrate.h"))
	return nil
}
