package ratesources

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vul-os/openrate/internal/rates"
)

// BISURLFmt is the BIS Stats SDMX REST endpoint for the central bank policy
// rates dataflow (WS_CBPOL), daily frequency, every reference area in one call.
// CSV is the simplest faithful format; lastNObservations bounds the history per
// series so a single request yields both the latest value and a usable
// timeseries for 40+ central banks worldwide — the open backbone for breadth.
//
// Columns: FREQ,REF_AREA,UNIT_MEASURE,UNIT_MULT,TIME_FORMAT,COMPILATION,
// DECIMALS,SOURCE_REF,SUPP_INFO_BREAKS,TITLE,TIME_PERIOD,OBS_VALUE,OBS_STATUS,
// OBS_CONF,OBS_PRE_BREAK
const BISURLFmt = "https://stats.bis.org/api/v1/data/WS_CBPOL/D..?lastNObservations=%d&format=csv"

// BIS column indices.
const (
	bisRefArea   = 1
	bisSourceRef = 7
	bisTitle     = 9
	bisTime      = 10
	bisValue     = 11
)

type BIS struct {
	URL    string
	Client *http.Client
}

func NewBIS() *BIS {
	n := 90
	if v := os.Getenv("OPENRATE_BIS_HISTORY"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	return &BIS{
		URL:    fmt.Sprintf(BISURLFmt, n),
		Client: &http.Client{Timeout: 40 * time.Second},
	}
}

func (b *BIS) Name() string { return "bis" }

func (b *BIS) Fetch(ctx context.Context) ([]rates.Observation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/csv")
	req.Header.Set("User-Agent", "openrate/interest-rates (+https://github.com/vul-os/openrate)")
	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bis: status %d", resp.StatusCode)
	}

	r := csv.NewReader(io.LimitReader(resp.Body, 32<<20))
	r.FieldsPerRecord = -1 // BIS rows carry free-text columns of varying width
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("bis: read header: %w", err)
	}
	if len(header) <= bisValue {
		return nil, fmt.Errorf("bis: unexpected header (%d cols)", len(header))
	}

	var obs []rates.Observation
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Skip a malformed row rather than abandoning the whole feed.
			continue
		}
		if len(rec) <= bisValue {
			continue
		}
		area := strings.ToUpper(strings.TrimSpace(rec[bisRefArea]))
		valStr := strings.TrimSpace(rec[bisValue])
		dateStr := strings.TrimSpace(rec[bisTime])
		if area == "" || valStr == "" || dateStr == "" {
			continue
		}
		val, perr := strconv.ParseFloat(valStr, 64)
		// BIS marks missing days (weekends/holidays, OBS_STATUS "M") with the
		// literal "NaN", which ParseFloat accepts without error. Drop any
		// non-finite value so it never reaches the snapshot or JSON encoder.
		if perr != nil || math.IsNaN(val) || math.IsInf(val, 0) {
			continue
		}
		date, derr := time.Parse("2006-01-02", dateStr)
		if derr != nil {
			continue
		}
		obs = append(obs, rates.Observation{
			Series: strings.ToLower(area) + ".policy",
			Area:   area,
			Type:   rates.TypePolicy,
			Name:   bisName(rec),
			Value:  val,
			Date:   date,
			Source: b.Name(),
		})
	}
	if len(obs) == 0 {
		return nil, fmt.Errorf("bis: no policy-rate observations parsed")
	}
	return obs, nil
}

// bisName derives a concise label from the TITLE column, e.g.
// "Central bank policy rates - United States - Daily - End of period" ->
// "United States — policy rate", falling back to the SOURCE_REF (issuing bank).
func bisName(rec []string) string {
	title := strings.TrimSpace(rec[bisTitle])
	parts := strings.Split(title, " - ")
	if len(parts) >= 2 {
		country := strings.TrimSpace(parts[1])
		if country != "" {
			return country + " — policy rate"
		}
	}
	if len(rec) > bisSourceRef {
		if src := strings.TrimSpace(rec[bisSourceRef]); src != "" {
			return src + " — policy rate"
		}
	}
	return "policy rate"
}
