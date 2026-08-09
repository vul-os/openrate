package fxsource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/vul-os/openrate/fx"
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

func (o *OXR) Fetch(ctx context.Context) ([]fx.Edge, error) {
	if o.Key == "" {
		return nil, fmt.Errorf("oxr: OPENRATE_OXR_APP_ID not set")
	}
	url := "https://openexchangerates.org/api/latest.json?app_id=" + o.Key
	// Never ignore this error: an app id carrying a control character fails
	// url.Parse, leaves req nil, and Client.Do(nil) panics inside the Refresher's
	// bare fetch goroutine. The message omits the parse error, which echoes the
	// full URL and therefore the credential.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("oxr: invalid request URL (check OPENRATE_OXR_APP_ID)")
	}
	resp, err := o.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oxr: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("oxr: read body: %w", err)
	}
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
	var edges []fx.Edge
	for code, rate := range r.Rates {
		if !fiatAllow[code] || rate <= 0 {
			continue
		}
		edges = append(edges, fx.Edge{From: base, To: code, Rate: rate, Source: o.Name(), Time: ts})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("oxr: no allowlisted rates")
	}
	return edges, nil
}
