package sources

import "strings"

// constructors maps a source name to its factory. Add new sources here.
var constructors = map[string]func() Source{
	"ecb":         func() Source { return NewECB() },
	"coinbase":    func() Source { return NewCoinbase() },
	"luno":        func() Source { return NewLuno() },
	"sarb":        func() Source { return NewSARB() },
	"frankfurter": func() Source { return NewFrankfurter() },
	"yahoo":       func() Source { return NewYahoo() },
}

// DefaultSources are enabled out of the box: verified free + open, and together
// they give daily fiat breadth (ecb), real-time fiat incl. ZAR (coinbase), and a
// live SA crypto/ZAR cross-check (luno). yahoo/frankfurter are opt-in.
var DefaultSources = []string{"ecb", "coinbase", "luno", "sarb"}

// Build resolves a comma/space separated list of names into Source instances,
// skipping unknown names. Empty input falls back to DefaultSources.
func Build(spec string) []Source {
	names := splitNames(spec)
	if len(names) == 0 {
		names = DefaultSources
	}
	var out []Source
	for _, n := range names {
		if mk, ok := constructors[n]; ok {
			out = append(out, mk())
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
