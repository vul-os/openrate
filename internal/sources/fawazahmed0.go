package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// FawazURL is the fawazahmed0 currency-api: a fully open, CDN-served daily feed
// (~400 currencies incl. crypto) with no key and no rate limits. Served from
// jsDelivr with a Cloudflare Pages fallback, so its uptime story is strong. Keys
// are lowercase ISO codes. USD base here.
const FawazURL = "https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/usd.json"

// FawazFallbackURL mirrors the same data if the CDN path fails.
const FawazFallbackURL = "https://currency-api.pages.dev/v1/currencies/usd.json"

type Fawaz struct {
	URL      string
	Fallback string
	Client   *http.Client
}

func NewFawaz() *Fawaz {
	return &Fawaz{URL: FawazURL, Fallback: FawazFallbackURL, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (f *Fawaz) Name() string { return "fawazahmed0" }

func (f *Fawaz) Fetch(ctx context.Context) ([]graph.Edge, error) {
	body, err := f.get(ctx, f.URL)
	if err != nil {
		body, err = f.get(ctx, f.Fallback)
		if err != nil {
			return nil, err
		}
	}
	// Shape: {"date":"2026-06-21","usd":{"eur":0.87,"zar":16.44,...}}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("fawazahmed0: parse: %w", err)
	}
	ts := time.Now().UTC()
	if d, ok := raw["date"]; ok {
		var ds string
		if json.Unmarshal(d, &ds) == nil {
			if t, e := time.Parse("2006-01-02", ds); e == nil {
				ts = t
			}
		}
	}
	rates := map[string]float64{}
	if err := json.Unmarshal(raw["usd"], &rates); err != nil {
		return nil, fmt.Errorf("fawazahmed0: parse usd: %w", err)
	}
	var edges []graph.Edge
	for code, rate := range rates {
		up := strings.ToUpper(code)
		if !allowed(up) || rate <= 0 {
			continue
		}
		edges = append(edges, graph.Edge{From: "USD", To: up, Rate: rate, Source: f.Name(), Time: ts})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("fawazahmed0: no allowlisted rates")
	}
	return edges, nil
}

func (f *Fawaz) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fawazahmed0: status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}
