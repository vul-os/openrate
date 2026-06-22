package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// LunoURL is Luno's public tickers endpoint: free, no auth, real-time. Luno is a
// South-African exchange, so its ZAR books (BTC/ZAR, ETH/ZAR) are a live,
// 24/7 ZAR signal. These edges bridge into the fiat graph through BTC/ETH, which
// Coinbase also quotes — giving an independent, crypto-routed ZAR cross that
// cross-checks the direct fiat one.
const LunoURL = "https://api.luno.com/api/1/tickers"

type Luno struct {
	URL    string
	Client *http.Client
}

func NewLuno() *Luno {
	return &Luno{URL: LunoURL, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (l *Luno) Name() string { return "luno" }

type lunoResp struct {
	Tickers []struct {
		Pair      string `json:"pair"`
		LastTrade string `json:"last_trade"`
		Timestamp int64  `json:"timestamp"` // ms
	} `json:"tickers"`
}

// normSym maps Luno's ticker symbols to openrate's node names (Luno uses XBT for
// bitcoin; we use BTC so it shares a node with Coinbase).
func normSym(s string) string {
	if s == "XBT" {
		return "BTC"
	}
	return s
}

func (l *Luno) Fetch(ctx context.Context) ([]graph.Edge, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, l.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("luno: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var lr lunoResp
	if err := json.Unmarshal(body, &lr); err != nil {
		return nil, fmt.Errorf("luno: parse: %w", err)
	}

	var edges []graph.Edge
	for _, t := range lr.Tickers {
		// Only keep ZAR-quoted pairs of allowlisted base assets (e.g. XBTZAR).
		if !strings.HasSuffix(t.Pair, "ZAR") {
			continue
		}
		base := normSym(strings.TrimSuffix(t.Pair, "ZAR"))
		if !allowed(base) {
			continue
		}
		price, perr := strconv.ParseFloat(t.LastTrade, 64)
		if perr != nil || price <= 0 {
			continue
		}
		ts := time.UnixMilli(t.Timestamp).UTC()
		if t.Timestamp == 0 {
			ts = time.Now().UTC()
		}
		// 1 base = price ZAR.
		edges = append(edges, graph.Edge{From: base, To: "ZAR", Rate: price, Source: l.Name(), Time: ts})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("luno: no ZAR pairs")
	}
	return edges, nil
}
