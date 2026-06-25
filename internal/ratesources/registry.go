package ratesources

import (
	"os"
	"strings"
)

// constructors maps an interest-rate source name to its factory. Add new
// sources here. Keep names lower-case and stable (they appear in the API).
var constructors = map[string]func() Source{
	"bis":       func() Source { return NewBIS() },
	"sarbrates": func() Source { return NewSARBRates() },
	"fred":      func() Source { return NewFRED() },
}

// paidKeyEnv maps each keyed source to the env var that enables it. If the var
// is set, the source is auto-added (no need to list it in -interest-sources):
// the same ".env and it just works" path the FX side uses for paid providers.
var paidKeyEnv = map[string]string{
	"fred": "OPENRATE_FRED_API_KEY",
}

// DefaultSources are enabled out of the box: verified free + open. BIS alone
// gives policy-rate breadth across 40+ central banks worldwide; sarbrates adds
// a directly-issued overnight reference series (ZARONIA) with deep history.
var DefaultSources = []string{"bis", "sarbrates"}

// Build resolves a comma/space separated list of names into Source instances,
// skipping unknown names. Empty input falls back to DefaultSources. Any keyed
// source whose env var is present is auto-enabled.
func Build(spec string) []Source {
	names := splitNames(spec)
	if len(names) == 0 {
		names = DefaultSources
	}
	chosen := map[string]bool{}
	var out []Source
	for _, n := range names {
		if mk, ok := constructors[n]; ok && !chosen[n] {
			out = append(out, mk())
			chosen[n] = true
		}
	}
	for name, env := range paidKeyEnv {
		if !chosen[name] && os.Getenv(env) != "" {
			out = append(out, constructors[name]())
			chosen[name] = true
		}
	}
	return out
}

func splitNames(spec string) []string {
	f := func(r rune) bool { return r == ',' || r == ' ' }
	var out []string
	for _, p := range strings.FieldsFunc(spec, f) {
		out = append(out, strings.ToLower(strings.TrimSpace(p)))
	}
	return out
}
