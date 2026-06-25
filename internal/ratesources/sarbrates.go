package ratesources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/vul-os/openrate/internal/rates"
)

// SARBRatesURL is the South African Reserve Bank's public rate-reform endpoint.
// It returns the ZARONIA overnight reference rate and its compounded averages as
// JSON, no auth — the authoritative, directly-issued domestic benchmark. This is
// a faithful port of the standalone amortini scraper's ZARONIA path, now as an
// openrate source so the series is available to any consumer.
//
// Response shape: {"rate_type":"ZARONIA","rates":[{"date":"2024-06-12T00:00:00Z",
// "rate":8.30,"WEEK_COMPOUND":...,"MONTH_1_COMPOUND":...,...,"ZARONIA_INDEX":...}]}
const SARBRatesURL = "https://www.resbank.co.za/bin/sarb/ratereform"

// zaroniaFields maps each JSON field to a canonical series. The overnight "rate"
// is the headline; the COMPOUND fields are term averages; the index is a level.
var zaroniaFields = []struct {
	field  string
	series string
	typ    string
	tenor  string
	name   string
}{
	{"rate", "za.ref.zaronia", rates.TypeReference, "ON", "South Africa — ZARONIA (overnight)"},
	{"WEEK_COMPOUND", "za.ref.zaronia.1w", rates.TypeReference, "1W", "South Africa — ZARONIA 1-week compounded"},
	{"MONTH_1_COMPOUND", "za.ref.zaronia.1m", rates.TypeReference, "1M", "South Africa — ZARONIA 1-month compounded"},
	{"MONTH_3_COMPOUND", "za.ref.zaronia.3m", rates.TypeReference, "3M", "South Africa — ZARONIA 3-month compounded"},
	{"MONTH_6_COMPOUND", "za.ref.zaronia.6m", rates.TypeReference, "6M", "South Africa — ZARONIA 6-month compounded"},
	{"MONTH_9_COMPOUND", "za.ref.zaronia.9m", rates.TypeReference, "9M", "South Africa — ZARONIA 9-month compounded"},
	{"MONTH_12_COMPOUND", "za.ref.zaronia.12m", rates.TypeReference, "12M", "South Africa — ZARONIA 12-month compounded"},
	{"ZARONIA_INDEX", "za.ref.zaronia.index", "index", "INDEX", "South Africa — ZARONIA cumulative index"},
}

type SARBRates struct {
	BaseURL string
	Days    int // history window in days
	Client  *http.Client
}

func NewSARBRates() *SARBRates {
	// The SARB host is slow and intermittently drops TCP connects; bound each dial
	// so a failed attempt fails fast and Fetch can retry within the store budget.
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 13 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &SARBRates{
		BaseURL: SARBRatesURL,
		Days:    180,
		Client:  &http.Client{Timeout: 25 * time.Second, Transport: transport},
	}
}

func (s *SARBRates) Name() string { return "sarbrates" }

type zaroniaResp struct {
	RateType string                       `json:"rate_type"`
	Rates    []map[string]json.RawMessage `json:"rates"`
}

func (s *SARBRates) Fetch(ctx context.Context) ([]rates.Observation, error) {
	now := time.Now().UTC()
	q := url.Values{}
	q.Set("start_date", now.AddDate(0, 0, -s.Days).Format("2006-01-02"))
	q.Set("end_date", now.Format("2006-01-02"))
	q.Set("rate_type", "ZARONIA")
	endpoint := s.BaseURL + "?" + q.Encode()

	var body []byte
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if ctx.Err() != nil {
			break
		}
		body, lastErr = s.get(ctx, endpoint)
		if lastErr == nil {
			break
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	var parsed zaroniaResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("sarbrates: parse: %w", err)
	}
	if len(parsed.Rates) == 0 {
		return nil, fmt.Errorf("sarbrates: no ZARONIA records returned")
	}

	var obs []rates.Observation
	for _, rec := range parsed.Rates {
		date, ok := zaroniaDate(rec)
		if !ok {
			continue
		}
		for _, f := range zaroniaFields {
			raw, ok := rec[f.field]
			if !ok {
				continue
			}
			var v float64
			if json.Unmarshal(raw, &v) != nil {
				continue
			}
			// Rates are bounded percentages; the index can exceed 100.
			if f.typ != "index" && (v < 0 || v > 100) {
				continue
			}
			obs = append(obs, rates.Observation{
				Series: f.series,
				Area:   "ZA",
				Type:   f.typ,
				Tenor:  f.tenor,
				Name:   f.name,
				Value:  v,
				Date:   date,
				Source: s.Name(),
			})
		}
	}
	if len(obs) == 0 {
		return nil, fmt.Errorf("sarbrates: no usable ZARONIA values")
	}
	return obs, nil
}

func zaroniaDate(rec map[string]json.RawMessage) (time.Time, bool) {
	raw, ok := rec["date"]
	if !ok {
		return time.Time{}, false
	}
	var ds string
	if json.Unmarshal(raw, &ds) != nil || ds == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, ds); err == nil {
		return t, true
	}
	// SARB returns ISO 8601 with a Z suffix; fall back to date-only.
	if len(ds) >= 10 {
		if t, err := time.Parse("2006-01-02", ds[:10]); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (s *SARBRates) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
		return nil, fmt.Errorf("sarbrates: status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
