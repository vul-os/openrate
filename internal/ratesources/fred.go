package ratesources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/vul-os/openrate/internal/rates"
)

// FREDBase is the Federal Reserve (St. Louis) FRED observations API. It requires
// a free API key (OPENRATE_FRED_API_KEY); when present, this source auto-enables
// and enriches the open BIS breadth with high-frequency US benchmark series the
// policy feed does not carry — the "bring your own key for more datapoints" path.
const FREDBase = "https://api.stlouisfed.org/fred/series/observations"

// fredSeries maps a FRED series id to a canonical openrate series. Curated,
// well-known US benchmarks; extend freely.
var fredSeries = []struct {
	id     string
	series string
	area   string
	typ    string
	tenor  string
	name   string
}{
	{"SOFR", "us.ref.sofr", "US", rates.TypeReference, "ON", "United States — SOFR (secured overnight financing rate)"},
	{"EFFR", "us.ref.effr", "US", rates.TypeReference, "ON", "United States — effective federal funds rate"},
	{"OBFR", "us.ref.obfr", "US", rates.TypeReference, "ON", "United States — overnight bank funding rate"},
	{"DPRIME", "us.lending.prime", "US", rates.TypeLending, "", "United States — bank prime loan rate"},
	{"DGS10", "us.bond.10y", "US", rates.TypeBond, "10Y", "United States — 10-year Treasury yield"},
	{"DGS2", "us.bond.2y", "US", rates.TypeBond, "2Y", "United States — 2-year Treasury yield"},
}

type FRED struct {
	Key    string
	Limit  int // observations per series
	Client *http.Client
}

func NewFRED() *FRED {
	limit := 120
	if v := os.Getenv("OPENRATE_FRED_HISTORY"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	return &FRED{
		Key:    os.Getenv("OPENRATE_FRED_API_KEY"),
		Limit:  limit,
		Client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (f *FRED) Name() string { return "fred" }

type fredResp struct {
	Observations []struct {
		Date  string `json:"date"`
		Value string `json:"value"`
	} `json:"observations"`
}

func (f *FRED) Fetch(ctx context.Context) ([]rates.Observation, error) {
	if f.Key == "" {
		return nil, fmt.Errorf("fred: OPENRATE_FRED_API_KEY not set")
	}
	var obs []rates.Observation
	var firstErr error
	for _, s := range fredSeries {
		got, err := f.fetchSeries(ctx, s.id)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, o := range got {
			obs = append(obs, rates.Observation{
				Series: s.series,
				Area:   s.area,
				Type:   s.typ,
				Tenor:  s.tenor,
				Name:   s.name,
				Value:  o.value,
				Date:   o.date,
				Source: f.Name(),
			})
		}
	}
	if len(obs) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("fred: no observations parsed")
	}
	return obs, nil
}

type fredPoint struct {
	date  time.Time
	value float64
}

func (f *FRED) fetchSeries(ctx context.Context, id string) ([]fredPoint, error) {
	q := url.Values{}
	q.Set("series_id", id)
	q.Set("api_key", f.Key)
	q.Set("file_type", "json")
	q.Set("sort_order", "desc")
	q.Set("limit", strconv.Itoa(f.Limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FREDBase+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fred %s: status %d", id, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var fr fredResp
	if err := json.Unmarshal(body, &fr); err != nil {
		return nil, fmt.Errorf("fred %s: parse: %w", id, err)
	}
	var out []fredPoint
	for _, o := range fr.Observations {
		if o.Value == "" || o.Value == "." { // FRED uses "." for missing
			continue
		}
		v, perr := strconv.ParseFloat(o.Value, 64)
		if perr != nil {
			continue
		}
		d, derr := time.Parse("2006-01-02", o.Date)
		if derr != nil {
			continue
		}
		out = append(out, fredPoint{date: d, value: v})
	}
	return out, nil
}
