package ratesources

import (
	"sort"
	"testing"
)

func names(srcs []Source) []string {
	out := make([]string, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, s.Name())
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBuildDefaultsWhenSpecIsEmpty(t *testing.T) {
	// No FRED key, so only the open defaults.
	t.Setenv("OPENRATE_FRED_API_KEY", "")
	for _, spec := range []string{"", "   ", ",", " , "} {
		got := names(Build(spec))
		if want := []string{"bis", "sarbrates"}; !equal(got, want) {
			t.Errorf("Build(%q) = %v, want %v", spec, got, want)
		}
	}
}

func TestBuildHonoursExplicitSpec(t *testing.T) {
	t.Setenv("OPENRATE_FRED_API_KEY", "")
	got := names(Build("sarbrates"))
	if want := []string{"sarbrates"}; !equal(got, want) {
		t.Errorf("Build(\"sarbrates\") = %v, want %v", got, want)
	}
}

func TestBuildAcceptsSpacesCaseAndDuplicates(t *testing.T) {
	t.Setenv("OPENRATE_FRED_API_KEY", "")
	got := names(Build(" BIS , bis  sarbrates "))
	if want := []string{"bis", "sarbrates"}; !equal(got, want) {
		t.Errorf("Build with mixed case/spacing/duplicates = %v, want %v", got, want)
	}
}

// A spec naming no known source disables the engine — cmd/openrate only mounts
// the interest routes when Build returns a non-empty slice, so this is the
// documented "off" switch (docs/configuration.md).
func TestBuildUnknownNamesDisableTheEngine(t *testing.T) {
	t.Setenv("OPENRATE_FRED_API_KEY", "")
	if got := Build("none"); len(got) != 0 {
		t.Errorf("Build(\"none\") = %v, want empty so the interest engine stays unmounted", names(got))
	}
	if got := Build("nosuchsource,alsonot"); len(got) != 0 {
		t.Errorf("unknown names must be skipped, got %v", names(got))
	}
}

func TestBuildAutoEnablesFREDWhenKeyPresent(t *testing.T) {
	t.Setenv("OPENRATE_FRED_API_KEY", "test-key")
	got := names(Build(""))
	if want := []string{"bis", "fred", "sarbrates"}; !equal(got, want) {
		t.Errorf("with a FRED key set, Build(\"\") = %v, want %v", got, want)
	}
}

func TestBuildDoesNotDuplicateAnExplicitlyListedKeyedSource(t *testing.T) {
	t.Setenv("OPENRATE_FRED_API_KEY", "test-key")
	got := names(Build("bis,fred"))
	if want := []string{"bis", "fred"}; !equal(got, want) {
		t.Errorf("fred listed explicitly AND auto-enabled must appear once: got %v, want %v", got, want)
	}
}

// Even when every named source is unknown, a present key still enables its
// source — the key is the stronger signal.
func TestBuildKeyedSourceSurvivesAnUnknownSpec(t *testing.T) {
	t.Setenv("OPENRATE_FRED_API_KEY", "test-key")
	got := names(Build("none"))
	if want := []string{"fred"}; !equal(got, want) {
		t.Errorf("Build(\"none\") with a FRED key = %v, want %v", got, want)
	}
}

// Guards against a registry entry that would nil-panic in Build.
func TestEveryConstructorProducesANamedSource(t *testing.T) {
	if len(constructors) == 0 {
		t.Fatal("the registry is empty; this test would verify nothing")
	}
	seen := map[string]bool{}
	for name, mk := range constructors {
		s := mk()
		if s == nil {
			t.Errorf("constructor %q returned nil", name)
			continue
		}
		if s.Name() != name {
			t.Errorf("constructor %q builds a source named %q; the store keys observations by Name(), so they must match", name, s.Name())
		}
		if seen[s.Name()] {
			t.Errorf("duplicate source name %q", s.Name())
		}
		seen[s.Name()] = true
	}
	if len(seen) != len(constructors) {
		t.Fatalf("checked %d of %d registered sources", len(seen), len(constructors))
	}
}

func TestEveryKeyedSourceIsRegistered(t *testing.T) {
	if len(paidKeyEnv) == 0 {
		t.Fatal("paidKeyEnv is empty; this test would verify nothing")
	}
	for name, env := range paidKeyEnv {
		if _, ok := constructors[name]; !ok {
			t.Errorf("keyed source %q (env %s) has no constructor; Build would panic when the key is set", name, env)
		}
	}
}

func TestDefaultSourcesAreAllRegistered(t *testing.T) {
	if len(DefaultSources) == 0 {
		t.Fatal("DefaultSources is empty; the interest engine would never start")
	}
	for _, name := range DefaultSources {
		if _, ok := constructors[name]; !ok {
			t.Errorf("DefaultSources names %q, which is not in the registry", name)
		}
	}
}
