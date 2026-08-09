package fxsource

import (
	"context"
	"strings"
	"testing"
)

// A credential is concatenated into the request URL raw by every keyed adapter.
// A key carrying a control character — a newline picked up by
// `export OPENRATE_POLYGON_KEY=$'abc\ndef'`, or a stray CR from a Windows .env —
// makes url.Parse fail. The four adapters below used to discard that error, so
// req stayed nil and http.Client.Do(nil) dereferenced it: a nil-pointer panic
// raised inside the Refresher's bare per-source fetch goroutine
// (refresh.go's `go func(src fxsource.Source)`), which has no recover and
// therefore takes the whole process down. A bad key in the environment must be
// an error from one source, not a crash of the server.
//
// The assertion is deliberately two-part: it must not panic, AND it must not
// echo the key. url.Parse's error quotes the entire URL, so returning it wrapped
// would have published the credential into Status.LastError — which /readyz and
// /api/v1/meta serve to unauthenticated callers.
func TestKeyedSourcesRejectUnparseableKeyWithoutPanicking(t *testing.T) {
	const badKey = "SECRET\nVALUE"

	cases := []struct {
		name string
		src  Source
	}{
		{"polygon", &Polygon{Key: badKey, Client: NewPolygon().Client}},
		{"oxr", &OXR{Key: badKey, Client: NewOXR().Client}},
		{"twelvedata", &TwelveData{Key: badKey, Client: NewTwelveData().Client}},
		{"tradermade", &TraderMade{Key: badKey, Client: NewTraderMade().Client}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s.Fetch PANICKED on an unparseable key (%v) — in production this "+
						"panic is raised in the Refresher's fetch goroutine and kills the process", tc.name, r)
				}
			}()
			edges, err := tc.src.Fetch(context.Background())
			if err == nil {
				t.Fatalf("%s.Fetch returned nil error for an unparseable key; got %d edges", tc.name, len(edges))
			}
			if strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("%s.Fetch echoed the credential in its error: %q — this reaches "+
					"Status.LastError, which /readyz and /api/v1/meta publish unauthenticated", tc.name, err)
			}
		})
	}
}
