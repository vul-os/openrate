package fxsource

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/vul-os/openrate/fx"
)

// YahooBaseURL is the prefix each chart request is built on; the symbol and
// query string are appended to it.
const YahooBaseURL = "https://query1.finance.yahoo.com/v8/finance/chart/"

// Yahoo ingests FX quotes from Yahoo Finance's unofficial v8 chart endpoint
// (query1.finance.yahoo.com). It is free and near-real-time (~1 min during
// market hours) but comes with real caveats: no SLA, aggressive per-IP rate
// limiting (HTTP 429), and — importantly — Yahoo's Terms prohibit automated
// extraction and its robots.txt disallows crawlers. Kept OFF by default; enable
// with -sources ONLY if your use is permitted. A symbol is "<BASE><QUOTE>=X",
// e.g. "USDZAR=X", and reads as "1 BASE = price QUOTE".
type Yahoo struct {
	Symbols []string // e.g. ["USDZAR=X","USDEUR=X"]
	BaseURL string   // defaults to YahooBaseURL
	Client  *http.Client
}

func NewYahoo() *Yahoo {
	syms := []string{"USDZAR=X", "USDEUR=X", "USDGBP=X", "USDJPY=X", "USDCHF=X", "USDAUD=X", "USDCAD=X"}
	return &Yahoo{Symbols: syms, BaseURL: YahooBaseURL, Client: newClient(15 * time.Second)}
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

// parseFXSymbol splits a Yahoo FX symbol into its base and quote currency.
// The wire form is exactly eight characters — three letters of base, three of
// quote, then the "=X" that marks a currency cross — so "USDZAR=X" yields
// ("USD", "ZAR"). Anything else is a configuration mistake, not FX data.
//
// The length this replaces demanded nine characters and then sliced sym[6:] for
// the suffix, a pair of conditions no string can satisfy at once: every symbol
// was skipped and Fetch reported "no quotes (rate-limited or blocked?)", so a
// dead adapter looked like an upstream throttle for as long as it shipped.
func parseFXSymbol(sym string) (base, quote string, ok bool) {
	if len(sym) != 8 || sym[6:] != "=X" {
		return "", "", false
	}
	for i := 0; i < 6; i++ {
		if sym[i] < 'A' || sym[i] > 'Z' {
			return "", "", false
		}
	}
	return sym[0:3], sym[3:6], true
}

func (y *Yahoo) Fetch(ctx context.Context) ([]fx.Edge, error) {
	var edges []fx.Edge
	usable := 0
	for _, sym := range y.Symbols {
		from, to, ok := parseFXSymbol(sym)
		if !ok {
			continue
		}
		usable++
		price, ts, err := y.quote(ctx, sym)
		if err != nil || price <= 0 {
			continue // tolerate per-symbol failures (rate limits)
		}
		edges = append(edges, fx.Edge{From: from, To: to, Rate: price, Source: y.Name(), Time: ts})
	}
	if usable == 0 {
		// Distinct from the throttle case on purpose: nothing was even asked of
		// Yahoo, so reporting an upstream problem would send the operator to the
		// wrong place. Name the real fault — the configured symbols.
		return nil, fmt.Errorf("yahoo: none of the %d configured symbols is a well-formed FX symbol (want \"USDZAR=X\": 3-letter base, 3-letter quote, \"=X\")", len(y.Symbols))
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("yahoo: no quotes (rate-limited or blocked?)")
	}
	return edges, nil
}

func (y *Yahoo) quote(ctx context.Context, sym string) (float64, time.Time, error) {
	base := y.BaseURL
	if base == "" {
		base = YahooBaseURL
	}
	url := base + sym + "?interval=1m&range=1d"
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
