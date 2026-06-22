package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// ERAPIURL is the open.er-api.com free endpoint (no key on this host). Unlike the
// ECB-derived feeds it updates daily **including weekends** (~00:00 UTC), so it
// fills the Friday→Monday gap that freezes ECB/Frankfurter. USD base.
const ERAPIURL = "https://open.er-api.com/v6/latest/USD"

type ERAPI struct {
	URL    string
	Client *http.Client
}

func NewERAPI() *ERAPI {
	return &ERAPI{URL: ERAPIURL, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (e *ERAPI) Name() string { return "erapi" }

type erapiResp struct {
	Result         string             `json:"result"`
	TimeLastUpdate int64              `json:"time_last_update_unix"`
	BaseCode       string             `json:"base_code"`
	Rates          map[string]float64 `json:"rates"`
}

func (e *ERAPI) Fetch(ctx context.Context) ([]graph.Edge, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("erapi: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var er erapiResp
	if err := json.Unmarshal(body, &er); err != nil {
		return nil, fmt.Errorf("erapi: parse: %w", err)
	}
	if er.Result != "success" {
		return nil, fmt.Errorf("erapi: result %q", er.Result)
	}
	base := er.BaseCode
	if base == "" {
		base = "USD"
	}
	ts := time.Now().UTC()
	if er.TimeLastUpdate > 0 {
		ts = time.Unix(er.TimeLastUpdate, 0).UTC()
	}
	var edges []graph.Edge
	for code, rate := range er.Rates {
		if !fiatAllow[code] || rate <= 0 {
			continue
		}
		edges = append(edges, graph.Edge{From: base, To: code, Rate: rate, Source: e.Name(), Time: ts})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("erapi: no allowlisted rates")
	}
	return edges, nil
}
