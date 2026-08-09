package sources

import (
	"testing"
	"time"

	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/internal/quality"
)

// classOf asks the quality model how it would grade a path whose only source is
// name, using the same public entry point the API uses.
func classOf(name string) string {
	now := time.Now().UTC()
	p := fx.Pair{Rate: 1, Hops: 1, AsOf: now, Sources: []string{name}}
	return quality.Assess("USD", "ZAR", p, nil, now).SourceClass
}

// Every source that can put an edge in the graph must have an authority rank in
// internal/quality. A source with no rank is not an error anywhere — it silently
// grades as "unknown" (x0.8), quietly downgrading every rate whose path touches
// it, and ACCURACY.md's source-class table would not mention it either.
//
// This is the one coupling between the source registry and the quality model, so
// it is asserted rather than left to review.
func TestEveryRegisteredSourceHasAQualityRank(t *testing.T) {
	if len(constructors) == 0 {
		t.Fatal("the source registry is empty; this test would verify nothing")
	}
	checked := 0
	for name := range constructors {
		checked++
		if cls := classOf(name); cls == "unknown" {
			t.Errorf("source %q is registered in constructors but has no rank in internal/quality.sourceRank: "+
				"every rate routed through it would be graded %q. Add it to sourceRank and to ACCURACY.md's source-class table.",
				name, cls)
		}
	}
	if checked != len(constructors) {
		t.Fatalf("checked %d of %d registered sources", checked, len(constructors))
	}
	t.Logf("verified a quality rank for all %d registered sources", checked)
}

// Every paid source must also be constructible, or setting its key would enable
// a nil source.
func TestEveryPaidSourceIsRegistered(t *testing.T) {
	if len(paidKeyEnv) == 0 {
		t.Fatal("paidKeyEnv is empty; this test would verify nothing")
	}
	for name, env := range paidKeyEnv {
		if _, ok := constructors[name]; !ok {
			t.Errorf("paid source %q (env %s) has no constructor; Build would panic when the key is set", name, env)
		}
	}
}
