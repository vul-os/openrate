package sources

import (
	"context"

	"github.com/vul-os/openrate/internal/graph"
)

// SARB is the South African Reserve Bank source — the authoritative domestic
// reference for ZAR pairs (what SA banks and SARS reference). Because openrate
// is ZAR-anchored, SARB edges should win for the headline ZAR pairs while ECB
// fills the long tail (rebased to ZAR via cross-rate in the graph).
//
// TODO(sarb): wire the real SARB feed. The bank publishes selected historical
// exchange rates through its web statistical query / web service rather than a
// single static file like the ECB; the fetcher needs to query the relevant
// series (ZAR/USD, ZAR/EUR, ZAR/GBP, …) and emit ZAR-base edges:
//
//	graph.Edge{From: "ZAR", To: "USD", Rate: r, Source: "sarb", Time: t}
//
// Until then this source is registered but inert, so the multi-source graph
// shape is in place and ZAR anchoring already works via ECB's EUR/ZAR edge.
type SARB struct{}

func NewSARB() *SARB { return &SARB{} }

func (s *SARB) Name() string { return "sarb" }

func (s *SARB) Fetch(ctx context.Context) ([]graph.Edge, error) {
	return nil, nil // inert until the real feed is wired — see TODO above
}
