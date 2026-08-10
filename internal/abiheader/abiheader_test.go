package abiheader

import (
	"strings"
	"testing"
)

// The generator is the only thing standing between a release and a C header
// that lies about the library it ships with, so its refusals have to be real
// refusals. Each case below is a way the header could be wrong that a naive
// "replace the first thing that looks like a version" would happily paper over.

const realistic = `#ifndef OPENRATE_H
#define OPENRATE_H

/*
 * Compare the two after loading:
 *
 *   if (strcmp(openrate_abi_version(), OPENRATE_ABI_VERSION) != 0) { ... }
 */
#define OPENRATE_ABI_VERSION "0.1.2"

const char *openrate_abi_version(void);
#endif
`

func TestRenderRewritesTheDefineAndNothingElse(t *testing.T) {
	out, err := Render([]byte(realistic), "0.1.6")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `#define OPENRATE_ABI_VERSION "0.1.6"`) {
		t.Errorf("the macro was not rewritten:\n%s", got)
	}
	if strings.Contains(got, `"0.1.2"`) {
		t.Errorf("the old version survives somewhere in the output:\n%s", got)
	}
	// The prose that explains how to use the macro mentions it by name. A
	// generator that rewrote every mention would destroy the documentation the
	// header exists to carry.
	if !strings.Contains(got, "if (strcmp(openrate_abi_version(), OPENRATE_ABI_VERSION) != 0)") {
		t.Errorf("the generator rewrote the surrounding prose:\n%s", got)
	}
	// Exactly one line changed.
	before, after := strings.Split(realistic, "\n"), strings.Split(got, "\n")
	if len(before) != len(after) {
		t.Fatalf("line count changed from %d to %d", len(before), len(after))
	}
	changed := 0
	for i := range before {
		if before[i] != after[i] {
			changed++
		}
	}
	if changed != 1 {
		t.Errorf("%d lines changed, want exactly 1", changed)
	}
}

func TestCheckAgreesWithRender(t *testing.T) {
	if _, ok, err := Check([]byte(realistic), "0.1.2"); err != nil || !ok {
		t.Errorf("Check on an up-to-date header: ok=%v err=%v, want true/nil", ok, err)
	}
	if _, ok, err := Check([]byte(realistic), "0.1.6"); err != nil || ok {
		t.Errorf("Check on a stale header: ok=%v err=%v, want false/nil", ok, err)
	}
	out, err := Render([]byte(realistic), "0.1.6")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, ok, err := Check(out, "0.1.6"); err != nil || !ok {
		t.Errorf("Check on Render's own output: ok=%v err=%v — generation is not idempotent, so "+
			"`go generate` would never converge and the no-op assertion could never pass", ok, err)
	}
}

func TestRenderRefusesAHeaderItCannotOwn(t *testing.T) {
	for _, tc := range []struct {
		name, header, version, want string
	}{
		{
			name:    "no macro at all",
			header:  "#ifndef OPENRATE_H\n#define OPENRATE_H\n#endif\n",
			version: "0.1.6",
			want:    "defines no OPENRATE_ABI_VERSION",
		},
		{
			// Appending a definition beside an existing one would leave a
			// consumer comparing against whichever the preprocessor saw first.
			name:    "defined twice",
			header:  realistic + "#define OPENRATE_ABI_VERSION \"9.9.9\"\n",
			version: "0.1.6",
			want:    "2 times",
		},
		{
			name:    "empty version",
			header:  realistic,
			version: "",
			want:    "empty",
		},
		{
			name:    "version that would break out of the string literal",
			header:  realistic,
			version: `0.1.6" /* `,
			want:    "cannot go in a C string literal",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Render([]byte(tc.header), tc.version)
			if err == nil {
				t.Fatalf("Render accepted it and produced:\n%s", out)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
			if out != nil {
				t.Error("Render returned bytes alongside an error; a caller that ignores the error " +
					"would write them over the real header")
			}
		})
	}
}

// A macro whose mention in prose is at the start of a line must still not be
// mistaken for the definition, or the generator would silently corrupt a
// comment and leave the real #define untouched.
func TestRenderIgnoresAMentionThatIsNotADefinition(t *testing.T) {
	header := "OPENRATE_ABI_VERSION is a macro.\n" +
		"#define OPENRATE_ABI_VERSION \"0.1.2\"\n"
	out, err := Render([]byte(header), "0.1.6")
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got := string(out); got != "OPENRATE_ABI_VERSION is a macro.\n#define OPENRATE_ABI_VERSION \"0.1.6\"\n" {
		t.Errorf("output:\n%s", got)
	}
}
