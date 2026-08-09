package fxsource

import (
	"os"
	"sort"
	"strings"
)

// constructors maps a source name to its factory. Add new sources here.
var constructors = map[string]func() Source{
	"ecb":         func() Source { return NewECB() },
	"coinbase":    func() Source { return NewCoinbase() },
	"luno":        func() Source { return NewLuno() },
	"sarb":        func() Source { return NewSARB() },
	"frankfurter": func() Source { return NewFrankfurter() },
	"yahoo":       func() Source { return NewYahoo() },
	"erapi":       func() Source { return NewERAPI() },
	"fawazahmed0": func() Source { return NewFawaz() },
	"boc":         func() Source { return NewBoC() },
	"oxr":         func() Source { return NewOXR() },
	"twelvedata":  func() Source { return NewTwelveData() },
	"polygon":     func() Source { return NewPolygon() },
	"tradermade":  func() Source { return NewTraderMade() },
}

// paidKeyEnv maps each paid source to the env var that enables it. FromEnv adds
// the source when the var is set (no need to list it in -sources); this is the
// ".env and it just works" path for the binary. Build never consults it — a
// library call must not change behaviour because of an environment variable the
// host process happens to carry.
var paidKeyEnv = map[string]string{
	"oxr":        "OPENRATE_OXR_APP_ID",
	"twelvedata": "OPENRATE_TWELVEDATA_KEY",
	"polygon":    "OPENRATE_POLYGON_KEY",
	"tradermade": "OPENRATE_TRADERMADE_KEY",
}

// DefaultSources are enabled out of the box: verified free + open. Together they
// give daily fiat breadth (ecb), real-time fiat incl. ZAR (coinbase), a live SA
// crypto/ZAR cross-check (luno), and the authoritative daily ZAR reference
// (sarb). Opt-in extras: frankfurter, erapi, fawazahmed0, boc, yahoo.
var DefaultSources = []string{"ecb", "coinbase", "luno", "sarb"}

// Build resolves a comma/space separated list of names into Source instances,
// skipping unknown names. Empty input falls back to DefaultSources.
//
// Build is pure: the same spec yields the same set on every machine. Sources
// that need an API key are included only if the spec names them, and they read
// their key when they fetch. For the "any key in the environment turns its
// source on" behaviour, call FromEnv.
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
	return out
}

// FromEnv is the explicit opt-in to environment configuration: it builds the
// set named by OPENRATE_SOURCES (or DefaultSources when that is unset) and then
// adds any paid source whose API-key variable is present.
//
// Nothing else in openrate reads the environment. A host that wants full
// control passes its own spec to Build, or its own []Source to the Refresher,
// and no variable can widen that set behind its back.
func FromEnv() []Source { return fromEnv(os.Getenv, os.Getenv("OPENRATE_SOURCES")) }

// FromEnvSpec is FromEnv with the spec supplied by the caller — a command-line
// flag, say — instead of read from OPENRATE_SOURCES. The key auto-enable rule
// still applies, which is what makes `openrate -sources ecb` with an OXR key in
// .env behave the way the binary has always behaved.
func FromEnvSpec(spec string) []Source { return fromEnv(os.Getenv, spec) }

// fromEnv is FromEnv with the lookup injected, so the auto-enable rule is
// testable without mutating the process environment. Paid sources are appended
// in a fixed order: an unordered map walk would make the source list — and so
// the /api/v1/meta payload — differ from run to run for no reason.
func fromEnv(getenv func(string) string, spec string) []Source {
	out := Build(spec)
	chosen := map[string]bool{}
	for _, s := range out {
		chosen[s.Name()] = true
	}
	paid := make([]string, 0, len(paidKeyEnv))
	for name := range paidKeyEnv {
		paid = append(paid, name)
	}
	sort.Strings(paid)
	for _, name := range paid {
		if !chosen[name] && getenv(paidKeyEnv[name]) != "" {
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
