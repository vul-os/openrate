package fxsource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vul-os/openrate/fx"
)

// Polygon — Polygon.io real-time FX. Set OPENRATE_POLYGON_KEY to enable. We pull
// the global forex snapshot (one request → all pairs) and take the bid/ask mid.
type Polygon struct {
	Key    string
	Client *http.Client
}

func NewPolygon() *Polygon {
	return &Polygon{Key: os.Getenv("OPENRATE_POLYGON_KEY"), Client: newClient(15 * time.Second)}
}

func (p *Polygon) Name() string { return "polygon" }

func (p *Polygon) Fetch(ctx context.Context) ([]fx.Edge, error) {
	if p.Key == "" {
		return nil, fmt.Errorf("polygon: OPENRATE_POLYGON_KEY not set")
	}
	url := "https://api.polygon.io/v2/snapshot/locale/global/markets/forex/tickers?apiKey=" + p.Key
	// Never ignore this error. The key is concatenated into the URL raw, so a key
	// carrying a control character (a newline from a copy-paste `export`) fails
	// url.Parse, leaves req nil, and Client.Do(nil) panics inside the Refresher's
	// bare fetch goroutine — taking the process down. The message deliberately
	// omits the parse error, which echoes the full URL and therefore the key.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("polygon: invalid request URL (check OPENRATE_POLYGON_KEY)")
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("polygon: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("polygon: read body: %w", err)
	}
	var r struct {
		Tickers []struct {
			Ticker    string `json:"ticker"` // "C:EURUSD"
			LastQuote struct {
				A float64 `json:"a"` // ask
				B float64 `json:"b"` // bid
				T int64   `json:"t"` // ns
			} `json:"lastQuote"`
		} `json:"tickers"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("polygon: parse: %w", err)
	}
	now := time.Now().UTC()
	var edges []fx.Edge
	for _, t := range r.Tickers {
		code := strings.TrimPrefix(t.Ticker, "C:")
		if len(code) != 6 {
			continue
		}
		from, to := code[:3], code[3:]
		if !allowed(from) || !allowed(to) {
			continue
		}
		mid := (t.LastQuote.A + t.LastQuote.B) / 2
		if mid <= 0 {
			continue
		}
		ts := now
		if t.LastQuote.T > 0 {
			ts = time.Unix(0, t.LastQuote.T).UTC()
		}
		edges = append(edges, fx.Edge{From: from, To: to, Rate: mid, Source: p.Name(), Time: ts})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("polygon: no allowlisted pairs")
	}
	return edges, nil
}
