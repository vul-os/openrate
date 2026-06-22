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

// TwelveData — real-time FX, generous free tier (800 req/day). Set
// OPENRATE_TWELVEDATA_KEY to enable. We batch all USD/<ccy> symbols into one
// request to stay within rate limits.
type TwelveData struct {
	Key    string
	Client *http.Client
}

func NewTwelveData() *TwelveData {
	return &TwelveData{Key: os.Getenv("OPENRATE_TWELVEDATA_KEY"), Client: &http.Client{Timeout: 15 * time.Second}}
}

func (t *TwelveData) Name() string { return "twelvedata" }

func (t *TwelveData) Fetch(ctx context.Context) ([]graph.Edge, error) {
	if t.Key == "" {
		return nil, fmt.Errorf("twelvedata: OPENRATE_TWELVEDATA_KEY not set")
	}
	var syms []string
	for code := range fiatAllow {
		if code != "USD" {
			syms = append(syms, "USD/"+code)
		}
	}
	url := "https://api.twelvedata.com/exchange_rate?symbol=" + strings.Join(syms, ",") + "&apikey=" + t.Key
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := t.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("twelvedata: status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))

	// Multi-symbol → map keyed by symbol; single → flat object.
	type quote struct {
		Symbol string  `json:"symbol"`
		Rate   float64 `json:"rate"`
	}
	now := time.Now().UTC()
	var edges []graph.Edge
	add := func(sym string, rate float64) {
		to := strings.TrimPrefix(sym, "USD/")
		if rate > 0 && fiatAllow[to] {
			edges = append(edges, graph.Edge{From: "USD", To: to, Rate: rate, Source: t.Name(), Time: now})
		}
	}
	var multi map[string]quote
	if err := json.Unmarshal(body, &multi); err == nil && len(multi) > 0 {
		for _, q := range multi {
			if q.Symbol != "" {
				add(q.Symbol, q.Rate)
			}
		}
	}
	if len(edges) == 0 { // try single-quote shape
		var single quote
		if json.Unmarshal(body, &single) == nil && single.Symbol != "" {
			add(single.Symbol, single.Rate)
		}
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("twelvedata: no rates (check key/quota)")
	}
	return edges, nil
}
