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

// Yahoo ingests FX quotes from Yahoo Finance's unofficial v8 chart endpoint
// (query1.finance.yahoo.com). It is free and near-real-time (~1 min during
// market hours) but comes with real caveats: no SLA, aggressive per-IP rate
// limiting (HTTP 429), and — importantly — Yahoo's Terms prohibit automated
// extraction and its robots.txt disallows crawlers. Kept OFF by default; enable
// with -sources ONLY if your use is permitted. Symbol "USD<CCY>=X" reads as
// "1 USD = price CCY".
type Yahoo struct {
	Symbols []string // e.g. ["USDZAR=X","USDEUR=X"]
	Client  *http.Client
}

func NewYahoo() *Yahoo {
	syms := []string{"USDZAR=X", "USDEUR=X", "USDGBP=X", "USDJPY=X", "USDCHF=X", "USDAUD=X", "USDCAD=X"}
	return &Yahoo{Symbols: syms, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (y *Yahoo) Name() string { return "yahoo" }

type yahooChart struct {
	Chart struct {
		Result []struct {
			Meta struct {
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				RegularMarketTime  int64   `json:"regularMarketTime"`
			} `json:"meta"`
		} `json:"result"`
		Error any `json:"error"`
	} `json:"chart"`
}

func (y *Yahoo) Fetch(ctx context.Context) ([]graph.Edge, error) {
	var edges []graph.Edge
	for _, sym := range y.Symbols {
		if len(sym) != 9 || sym[6:] != "=X" { // "USDZAR=X"
			continue
		}
		to := sym[3:6]
		price, ts, err := y.quote(ctx, sym)
		if err != nil || price <= 0 {
			continue // tolerate per-symbol failures (rate limits)
		}
		edges = append(edges, graph.Edge{From: "USD", To: to, Rate: price, Source: y.Name(), Time: ts})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("yahoo: no quotes (rate-limited or blocked?)")
	}
	return edges, nil
}

func (y *Yahoo) quote(ctx context.Context, sym string) (float64, time.Time, error) {
	url := "https://query1.finance.yahoo.com/v8/finance/chart/" + sym + "?interval=1m&range=1d"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, time.Time{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120 Safari/537.36")
	resp, err := y.Client.Do(req)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, time.Time{}, fmt.Errorf("yahoo %s: status %d", sym, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, time.Time{}, err
	}
	var yc yahooChart
	if err := json.Unmarshal(body, &yc); err != nil {
		return 0, time.Time{}, err
	}
	if len(yc.Chart.Result) == 0 {
		return 0, time.Time{}, fmt.Errorf("yahoo %s: empty", sym)
	}
	m := yc.Chart.Result[0].Meta
	ts := time.Now().UTC()
	if m.RegularMarketTime > 0 {
		ts = time.Unix(m.RegularMarketTime, 0).UTC()
	}
	return m.RegularMarketPrice, ts, nil
}
