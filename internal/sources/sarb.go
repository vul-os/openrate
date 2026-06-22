package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// SARBURL is the South African Reserve Bank's public Web API. It returns the
// official daily ZAR reference rates as JSON with no auth — the authoritative
// domestic source (what SA banks and SARS reference). openrate is ZAR-anchored,
// so these edges should win for the headline ZAR pairs.
//
// Caveat: the SARB host can be slow to connect and rejects bare clients; we send
// a browser User-Agent and rely on the store's per-fetch timeout + retry cadence.
const SARBURL = "https://custom.resbank.co.za/SarbWebApi/WebIndicators/HomePageRates"

// sarbCodes maps SARB TimeseriesCodes (stable) to ISO currency codes. Each entry
// is "Rand per <currency>", i.e. value = ZAR per 1 unit of that currency.
var sarbCodes = map[string]string{
	"EXCX135D": "USD",
	"EXCZ001D": "GBP",
	"EXCZ002D": "EUR",
	"EXCZ120D": "JPY",
}

type SARB struct {
	URL    string
	Client *http.Client
}

func NewSARB() *SARB {
	// The SARB host is slow and intermittently drops TCP connects (i/o timeout).
	// Bound each dial to ~13s so a failed attempt fails fast, and let Fetch retry
	// a few times — three bounded attempts fit inside the store's per-fetch budget.
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 13 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &SARB{URL: SARBURL, Client: &http.Client{Timeout: 20 * time.Second, Transport: transport}}
}

func (s *SARB) Name() string { return "sarb" }

type sarbItem struct {
	Name           string  `json:"Name"`
	TimeseriesCode string  `json:"TimeseriesCode"`
	Date           string  `json:"Date"`
	Value          float64 `json:"Value"`
}

func (s *SARB) Fetch(ctx context.Context) ([]graph.Edge, error) {
	var body []byte
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			break
		}
		body, lastErr = s.get(ctx)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	var items []sarbItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("sarb: parse: %w", err)
	}

	var edges []graph.Edge
	for _, it := range items {
		ccy, ok := sarbCodes[it.TimeseriesCode]
		if !ok || it.Value <= 0 {
			continue
		}
		ts, perr := time.Parse("2006-01-02", it.Date)
		if perr != nil {
			// SARB sometimes returns RFC3339-ish dates; fall back to now.
			if t2, e2 := time.Parse(time.RFC3339, it.Date); e2 == nil {
				ts = t2
			} else {
				ts = time.Now().UTC()
			}
		}
		// value = ZAR per 1 CCY  =>  1 CCY = value ZAR.
		edges = append(edges, graph.Edge{From: ccy, To: "ZAR", Rate: it.Value, Source: s.Name(), Time: ts})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("sarb: no recognised ZAR series")
	}
	return edges, nil
}

// get performs a single HTTP attempt against the SARB Web API.
func (s *SARB) get(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sarb: status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
