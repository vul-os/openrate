package sources

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/vul-os/openrate/internal/graph"
)

// ECBDailyURL is the European Central Bank's daily reference file: a plain XML
// document, not an API, updated once per business day ~16:00 CET. It is the
// canonical free fiat source and the backbone of most "free" rate APIs.
const ECBDailyURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"

// ECB ingests the daily file as EUR-base edges (1 EUR = rate CCY).
type ECB struct {
	URL    string
	Client *http.Client
}

func NewECB() *ECB {
	return &ECB{URL: ECBDailyURL, Client: &http.Client{Timeout: 15 * time.Second}}
}

func (e *ECB) Name() string { return "ecb" }

// ecbEnvelope mirrors the eurofxref-daily.xml shape: a Cube nest where the dated
// Cube holds one <Cube currency=".." rate=".."/> per quoted currency.
type ecbEnvelope struct {
	Cubes []struct {
		Time  string `xml:"time,attr"`
		Rates []struct {
			Currency string `xml:"currency,attr"`
			Rate     string `xml:"rate,attr"`
		} `xml:"Cube"`
	} `xml:"Cube>Cube"`
}

func (e *ECB) Fetch(ctx context.Context) ([]graph.Edge, error) {
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
		return nil, fmt.Errorf("ecb: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	var env ecbEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("ecb: parse: %w", err)
	}

	var edges []graph.Edge
	for _, cube := range env.Cubes {
		ts, perr := time.Parse("2006-01-02", cube.Time)
		if perr != nil {
			ts = time.Now().UTC()
		}
		for _, r := range cube.Rates {
			rate, rerr := strconv.ParseFloat(r.Rate, 64)
			if rerr != nil || rate <= 0 || r.Currency == "" {
				continue
			}
			edges = append(edges, graph.Edge{
				From: "EUR", To: r.Currency, Rate: rate, Source: e.Name(), Time: ts,
			})
		}
	}
	if len(edges) == 0 {
		return nil, fmt.Errorf("ecb: no rates parsed")
	}
	return edges, nil
}
