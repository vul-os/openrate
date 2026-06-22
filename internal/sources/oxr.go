package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// OXR — Open Exchange Rates. Paid (free tier hourly; ~$12/mo for 60s updates),
// 200+ currencies, USD base. Set OPENRATE_OXR_APP_ID to enable. Adds broad,
// frequently-updated corroboration that lifts grades on the long tail.
type OXR struct {
	Key    string
	Client *http.Client
}

func NewOXR() *OXR {
	return &OXR{Key: os.Getenv("OPENRATE_OXR_APP_ID"), Client: &http.Client{Timeout: 15 * time.Second}}
}

func (o *OXR) Name() string { return "oxr" }

func (o *OXR) Fetch(ctx context.Context) ([]graph.Edge, error) {
	if o.Key == "" {
		return nil, fmt.Errorf("oxr: OPENRATE_OXR_APP_ID not set")
	}
	url := "https://openexchangerates.org/api/latest.json?app_id=" + o.Key
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oxr: status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var r struct {
		Timestamp int64              `json:"timestamp"`
		Base      string             `json:"base"`
		Rates     map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("oxr: parse: %w", err)
	}
	base := r.Base
	if base == "" {
		base = "USD"
	}
	ts := time.Now().UTC()
	if r.Timestamp > 0 {
		ts = time.Unix(r.Timestamp, 0).UTC()
	}
	var edges []graph.Edge
	for code, rate := range r.Rates {
		if !fiatAllow[code] || rate <= 0 {
			continue
		}
		edges = append(edges, graph.Edge{From: base, To: code, Rate: rate, Source: o.Name(), Time: ts})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("oxr: no allowlisted rates")
	}
	return edges, nil
}
