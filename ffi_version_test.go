package openrate_test

// The ABI version is compiled into the shared library, so it cannot be read
// from VERSION at runtime and has to be duplicated. ffi/ pins that duplication
// with TestVersionMatchesTheVERSIONFile — but ffi/ is a SEPARATE Go module, so
// `go test ./...` here never runs it.
//
// That gap shipped: v0.1.3 was tagged and pushed with VERSION at 0.1.3 and
// ffi/abi/version.go still at 0.1.2, and every check run before tagging was
// green, because none of them could see across the module boundary. A library
// built from that tag would have told every host it was 0.1.2 — breaking the
// staleness detection openrate_abi_version() exists to provide, in the
// direction that silently reports "old" for a current library.
//
// This test lives in the ROOT module on purpose. It reads ffi/'s source as
// text rather than importing it, because importing across the boundary is
// exactly what is not possible.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestFFIABIVersionTracksTheVERSIONFile(t *testing.T) {
	want, err := os.ReadFile("VERSION")
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	version := strings.TrimSpace(string(want))
	if version == "" {
		t.Fatal("VERSION is empty, so this test would compare nothing against nothing")
	}

	for _, tc := range []struct {
		file string
		re   *regexp.Regexp
		what string
	}{
		{"ffi/abi/version.go", regexp.MustCompile(`(?m)^const Version = "([^"]+)"`), "the Go constant"},
		{"ffi/include/openrate.h", regexp.MustCompile(`(?m)^#define OPENRATE_ABI_VERSION "([^"]+)"`), "the C header macro"},
	} {
		src, err := os.ReadFile(tc.file)
		if err != nil {
			t.Errorf("read %s: %v", tc.file, err)
			continue
		}
		m := tc.re.FindSubmatch(src)
		if m == nil {
			t.Errorf("%s: could not find %s. If it was renamed, this check is now "+
				"examining nothing — fix the pattern rather than deleting the test.", tc.file, tc.what)
			continue
		}
		if got := string(m[1]); got != version {
			t.Errorf("%s declares %q but VERSION says %q.\n"+
				"A release that bumps one and not the other ships a library that "+
				"misreports itself to every host that asks.", tc.file, got, version)
		}
	}
}
