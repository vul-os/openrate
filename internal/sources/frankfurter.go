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

// FrankfurterURL is the Frankfurter public API: a clean JSON mirror of the ECB
// daily reference rates (EUR base). Free, no auth, no quota. It overlaps with the
// direct ECB source, so it is off by default — useful as a drop-in if you prefer
// JSON over parsing the ECB XML, or as a redundancy.
const FrankfurterURL = "https://api.frankfurter.dev/v1/latest"

type Frankfurter struct {
	URL    string
	Client *http.Client
}

func NewFrankfurter() *Frankfurter {
	return &Frankfurter{URL: FrankfurterURL, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (f *Frankfurter) Name() string { return "frankfurter" }

type frankResp struct {
	Base  string             `json:"base"`
	Date  string             `json:"date"`
	Rates map[string]float64 `json:"rates"`
}

func (f *Frankfurter) Fetch(ctx context.Context) ([]graph.Edge, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("frankfurter: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var fr frankResp
	if err := json.Unmarshal(body, &fr); err != nil {
		return nil, fmt.Errorf("frankfurter: parse: %w", err)
	}
	if fr.Base == "" {
		fr.Base = "EUR"
	}
	ts, perr := time.Parse("2006-01-02", fr.Date)
	if perr != nil {
		ts = time.Now().UTC()
	}
	var edges []graph.Edge
	for code, rate := range fr.Rates {
		if rate <= 0 {
			continue
		}
		edges = append(edges, graph.Edge{From: fr.Base, To: code, Rate: rate, Source: f.Name(), Time: ts})
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("frankfurter: no rates")
	}
	return edges, nil
}
