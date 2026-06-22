package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// CoinbaseURL is Coinbase's public exchange-rates endpoint: free, no auth, and
// updated continuously. It returns USD-base rates for ~hundreds of assets,
// including real fiat (EUR, GBP, ZAR, …). This is openrate's best *open*
// intraday fiat source — it moves through the weekend and carries ZAR directly.
const CoinbaseURL = "https://api.coinbase.com/v2/exchange-rates?currency=USD"

type Coinbase struct {
	URL    string
	Client *http.Client
}

func NewCoinbase() *Coinbase {
	return &Coinbase{URL: CoinbaseURL, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Coinbase) Name() string { return "coinbase" }

type coinbaseResp struct {
	Data struct {
		Currency string            `json:"currency"`
		Rates    map[string]string `json:"rates"`
	} `json:"data"`
}

// Fetch emits USD-base edges (1 USD = rate CCY) for the allowlisted currencies.
// Coinbase gives no timestamp; the feed is live, so we stamp now — which is the
// point: these edges read as seconds-old next to ECB's day-old reference.
func (c *Coinbase) Fetch(ctx context.Context) ([]graph.Edge, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("coinbase: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var cr coinbaseResp
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("coinbase: parse: %w", err)
	}

	now := time.Now().UTC()
	var edges []graph.Edge
	for code, s := range cr.Data.Rates {
		if !allowed(code) {
			continue
		}
		rate, perr := strconv.ParseFloat(s, 64)
		if perr != nil || rate <= 0 {
			continue
		}
		edges = append(edges, graph.Edge{From: "USD", To: code, Rate: rate, Source: c.Name(), Time: now})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("coinbase: no allowlisted rates")
	}
	return edges, nil
}
