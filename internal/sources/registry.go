package sources

import (
	"os"
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

// paidKeyEnv maps each paid source to the env var that enables it. If the var is
// set, the source is auto-added (no need to list it in -sources). This is the
// ".env and it just works" path for the OSS project.
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
	// Auto-enable any paid source whose API key is present in the environment.
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
