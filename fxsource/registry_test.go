package fxsource

import (
	"slices"
	"testing"
)

func names(ss []Source) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Name())
	}
	return out
}

func TestBuildResolvesNamesAndSkipsUnknown(t *testing.T) {
	got := names(Build("ecb, coinbase,nope ecb"))
	want := []string{"ecb", "coinbase"}
	if !slices.Equal(got, want) {
		t.Fatalf("Build = %v, want %v (unknown names skipped, duplicates collapsed, order preserved)", got, want)
	}
	if got := names(Build("")); !slices.Equal(got, DefaultSources) {
		t.Fatalf("Build(\"\") = %v, want the defaults %v", got, DefaultSources)
	}
	if got := names(Build("nope,alsonope")); len(got) != 0 {
		t.Fatalf("Build of only-unknown names = %v, want nothing", got)
	}
}

// TestBuildIgnoresTheEnvironment is the fix this file exists for. Build used to
// scan the environment and silently add any paid source whose key was present,
// so the same call returned a different set on two machines and an embedding
// host could acquire an outbound dependency — and a bill — it never asked for.
func TestBuildIgnoresTheEnvironment(t *testing.T) {
	t.Setenv("OPENRATE_OXR_APP_ID", "secret")
	t.Setenv("OPENRATE_POLYGON_KEY", "secret")
	t.Setenv("OPENRATE_SOURCES", "ecb,coinbase,luno,sarb,yahoo")

	for _, spec := range []string{"", "ecb", "ecb,coinbase"} {
		got := names(Build(spec))
		for _, paid := range []string{"oxr", "polygon", "twelvedata", "tradermade"} {
			if slices.Contains(got, paid) {
				t.Errorf("Build(%q) = %v — it added %q from the environment", spec, got, paid)
			}
		}
	}
	// And it did not silently take the spec from the environment either.
	if got := names(Build("")); !slices.Equal(got, DefaultSources) {
		t.Errorf(`Build("") = %v with OPENRATE_SOURCES set — it read the variable`, got)
	}
}

// FromEnv is where that behaviour lives now, and it must actually still work:
// moving a feature and quietly dropping it would be the worse bug.
func TestFromEnvReadsSpecAndKeys(t *testing.T) {
	env := map[string]string{
		"OPENRATE_SOURCES":        "ecb",
		"OPENRATE_POLYGON_KEY":    "k1",
		"OPENRATE_TWELVEDATA_KEY": "k2",
	}
	got := names(fromEnv(func(k string) string { return env[k] }, env["OPENRATE_SOURCES"]))

	want := []string{"ecb", "polygon", "twelvedata"}
	if !slices.Equal(got, want) {
		t.Fatalf("fromEnv = %v, want %v (spec first, then keyed paid sources in sorted order)", got, want)
	}
}

// The paid set is appended in a fixed order. It used to come out of a map walk,
// which made /api/v1/meta list its sources differently on every restart.
func TestFromEnvOrderIsDeterministic(t *testing.T) {
	env := map[string]string{
		"OPENRATE_TRADERMADE_KEY": "k",
		"OPENRATE_OXR_APP_ID":     "k",
		"OPENRATE_POLYGON_KEY":    "k",
		"OPENRATE_TWELVEDATA_KEY": "k",
	}
	getenv := func(k string) string { return env[k] }

	first := names(fromEnv(getenv, "ecb"))
	if want := []string{"ecb", "oxr", "polygon", "tradermade", "twelvedata"}; !slices.Equal(first, want) {
		t.Fatalf("fromEnv = %v, want %v", first, want)
	}
	for range 20 {
		if got := names(fromEnv(getenv, "ecb")); !slices.Equal(got, first) {
			t.Fatalf("fromEnv returned %v then %v — the order is not stable", first, got)
		}
	}
}

// A source already named in the spec must not be added twice by the key rule.
func TestFromEnvDoesNotDuplicate(t *testing.T) {
	env := map[string]string{"OPENRATE_OXR_APP_ID": "k"}
	got := names(fromEnv(func(k string) string { return env[k] }, "ecb,oxr"))
	if want := []string{"ecb", "oxr"}; !slices.Equal(got, want) {
		t.Fatalf("fromEnv = %v, want %v", got, want)
	}
}

// Every registered constructor must produce a source whose Name matches its
// registry key — the key auto-enable rule dedupes on Name, and the graph keys a
// source's edges by it, so a mismatch would double-fetch and never replace.
func TestConstructorNamesMatchTheirKeys(t *testing.T) {
	for key, mk := range constructors {
		if got := mk().Name(); got != key {
			t.Errorf("constructors[%q] builds a source called %q", key, got)
		}
	}
	if len(constructors) < 13 {
		t.Fatalf("only %d constructors registered — this test is checking almost nothing", len(constructors))
	}
	for name := range paidKeyEnv {
		if _, ok := constructors[name]; !ok {
			t.Errorf("paidKeyEnv names %q, which has no constructor", name)
		}
	}
}
