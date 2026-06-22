package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// TraderMade — FX-specialist live quotes. Set OPENRATE_TRADERMADE_KEY to enable.
// We batch USD<ccy> pairs into one /live request and take the mid.
type TraderMade struct {
	Key    string
	Client *http.Client
}

func NewTraderMade() *TraderMade {
	return &TraderMade{Key: os.Getenv("OPENRATE_TRADERMADE_KEY"), Client: &http.Client{Timeout: 15 * time.Second}}
}

func (t *TraderMade) Name() string { return "tradermade" }

func (t *TraderMade) Fetch(ctx context.Context) ([]graph.Edge, error) {
	if t.Key == "" {
		return nil, fmt.Errorf("tradermade: OPENRATE_TRADERMADE_KEY not set")
	}
	var pairs []string
	for code := range fiatAllow {
		if code != "USD" {
			pairs = append(pairs, "USD"+code)
		}
	}
	url := "https://marketdata.tradermade.com/api/v1/live?currency=" + strings.Join(pairs, ",") + "&api_key=" + t.Key
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tradermade: status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var r struct {
		Quotes []struct {
			Base  string  `json:"base_currency"`
			Quote string  `json:"quote_currency"`
			Mid   float64 `json:"mid"`
		} `json:"quotes"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("tradermade: parse: %w", err)
	}
	now := time.Now().UTC()
	var edges []graph.Edge
	for _, q := range r.Quotes {
		if q.Mid <= 0 || !allowed(q.Base) || !allowed(q.Quote) {
			continue
		}
		edges = append(edges, graph.Edge{From: q.Base, To: q.Quote, Rate: q.Mid, Source: t.Name(), Time: now})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("tradermade: no quotes")
	}
	return edges, nil
}
