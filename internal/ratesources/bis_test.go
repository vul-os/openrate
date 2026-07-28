package ratesources

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The BIS CSV parser is the widest-reach ingest path in the repo — one request
// populates the policy rate for every reference area — and it takes free-text,
// ragged, third-party CSV. These tests drive it from realistic wire bytes.

// bisHeader mirrors the real WS_CBPOL CSV header (15 columns).
const bisHeader = "FREQ,REF_AREA,UNIT_MEASURE,UNIT_MULT,TIME_FORMAT,COMPILATION,DECIMALS,SOURCE_REF,SUPP_INFO_BREAKS,TITLE,TIME_PERIOD,OBS_VALUE,OBS_STATUS,OBS_CONF,OBS_PRE_BREAK"

// bisRow builds one data row with the columns the parser reads.
func bisRow(area, sourceRef, title, period, value string) string {
	cols := make([]string, 15)
	cols[0] = "D"
	cols[bisRefArea] = area
	cols[bisSourceRef] = sourceRef
	cols[bisTitle] = title
	cols[bisTime] = period
	cols[bisValue] = value
	return strings.Join(cols, ",")
}

func bisServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fetchBIS(t *testing.T, body string) ([]struct {
	Series string
	Area   string
	Value  float64
	Name   string
}, error) {
	t.Helper()
	srv := bisServer(t, 200, body)
	b := NewBIS()
	b.URL = srv.URL
	obs, err := b.Fetch(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]struct {
		Series string
		Area   string
		Value  float64
		Name   string
	}, 0, len(obs))
	for _, o := range obs {
		out = append(out, struct {
			Series string
			Area   string
			Value  float64
			Name   string
		}{o.Series, o.Area, o.Value, o.Name})
	}
	return out, nil
}

func TestBISParsesPolicyRates(t *testing.T) {
	body := strings.Join([]string{
		bisHeader,
		bisRow("US", "Federal Reserve", "Central bank policy rates - United States - Daily - End of period", "2026-06-16", "3.625"),
		bisRow("za", "SARB", "Central bank policy rates - South Africa - Daily - End of period", "2026-06-16", "7.25"),
	}, "\n")

	got, err := fetchBIS(t, body)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 observations, got %d: %+v", len(got), got)
	}

	byArea := map[string]struct {
		Series string
		Area   string
		Value  float64
		Name   string
	}{}
	for _, o := range got {
		byArea[o.Area] = o
	}

	us, ok := byArea["US"]
	if !ok {
		t.Fatalf("no US observation in %+v", got)
	}
	if us.Series != "us.policy" {
		t.Errorf("US series id = %q, want %q", us.Series, "us.policy")
	}
	if us.Value != 3.625 {
		t.Errorf("US value = %v, want 3.625", us.Value)
	}
	if us.Name != "United States — policy rate" {
		t.Errorf("US name = %q, want %q", us.Name, "United States — policy rate")
	}

	// The area column must be normalised: lower-case input yields an upper-case
	// Area and a lower-case series id, or the API's ?area= filter misses it.
	za, ok := byArea["ZA"]
	if !ok {
		t.Fatalf("lower-case area 'za' must normalise to 'ZA'; got %+v", got)
	}
	if za.Series != "za.policy" {
		t.Errorf("ZA series id = %q, want %q", za.Series, "za.policy")
	}
}

// TestBISDropsNonFiniteValues pins the behaviour SOURCES.md documents: BIS marks
// missing days with the literal text "NaN", which strconv.ParseFloat accepts
// without an error. Such a row must be skipped, not carried into the snapshot.
func TestBISDropsNonFiniteValues(t *testing.T) {
	body := strings.Join([]string{
		bisHeader,
		bisRow("US", "Fed", "Central bank policy rates - United States - Daily", "2026-06-16", "3.625"),
		bisRow("GB", "BoE", "Central bank policy rates - United Kingdom - Daily", "2026-06-14", "NaN"),
		bisRow("JP", "BoJ", "Central bank policy rates - Japan - Daily", "2026-06-14", "Inf"),
		bisRow("CH", "SNB", "Central bank policy rates - Switzerland - Daily", "2026-06-14", "-Inf"),
	}, "\n")

	got, err := fetchBIS(t, body)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("only the finite US row may survive; got %d: %+v", len(got), got)
	}
	for _, o := range got {
		if math.IsNaN(o.Value) || math.IsInf(o.Value, 0) {
			t.Errorf("%s carried a non-finite value %v", o.Series, o.Value)
		}
	}
}

func TestBISSkipsUnusableRows(t *testing.T) {
	body := strings.Join([]string{
		bisHeader,
		bisRow("US", "Fed", "Central bank policy rates - United States - Daily", "2026-06-16", "3.625"),
		bisRow("", "Fed", "no area", "2026-06-16", "1.0"),           // empty area
		bisRow("FR", "BdF", "no value", "2026-06-16", ""),           // empty value
		bisRow("DE", "Bbk", "no date", "", "1.0"),                   // empty date
		bisRow("IT", "BdI", "bad date", "16/06/2026", "1.0"),        // unparseable date
		bisRow("ES", "BdE", "bad value", "2026-06-16", "not-a-num"), // unparseable value
		"D,PT,short,row", // too few columns
	}, "\n")

	got, err := fetchBIS(t, body)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 || got[0].Area != "US" {
		t.Fatalf("only the well-formed US row may survive; got %+v", got)
	}
}

func TestBISNameFallsBackToSourceRef(t *testing.T) {
	// A TITLE with no " - " separator cannot yield a country, so the issuing
	// bank in SOURCE_REF is used instead.
	body := strings.Join([]string{
		bisHeader,
		bisRow("KW", "Central Bank of Kuwait", "PolicyRate", "2026-06-16", "4.0"),
	}, "\n")
	got, err := fetchBIS(t, body)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 observation, got %+v", got)
	}
	if want := "Central Bank of Kuwait — policy rate"; got[0].Name != want {
		t.Errorf("name = %q, want %q", got[0].Name, want)
	}
}

func TestBISNameFinalFallback(t *testing.T) {
	body := strings.Join([]string{
		bisHeader,
		bisRow("XX", "", "", "2026-06-16", "1.5"),
	}, "\n")
	got, err := fetchBIS(t, body)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got) != 1 || got[0].Name != "policy rate" {
		t.Errorf("want the bare fallback name, got %+v", got)
	}
}

func TestBISErrorsOnNonOKStatus(t *testing.T) {
	srv := bisServer(t, http.StatusServiceUnavailable, "down")
	b := NewBIS()
	b.URL = srv.URL
	if _, err := b.Fetch(context.Background()); err == nil {
		t.Fatal("a 503 must be an error, not an empty success")
	}
}

// TestBISErrorsWhenNothingParses matters because the store keeps a source's
// previous edges when a fetch errors. Returning (nil, nil) would instead wipe
// every policy rate the moment BIS changed its CSV layout.
func TestBISErrorsWhenNothingParses(t *testing.T) {
	body := strings.Join([]string{
		bisHeader,
		bisRow("GB", "BoE", "Central bank policy rates - United Kingdom", "2026-06-14", "NaN"),
	}, "\n")
	srv := bisServer(t, 200, body)
	b := NewBIS()
	b.URL = srv.URL
	if _, err := b.Fetch(context.Background()); err == nil {
		t.Fatal("a feed that yields zero observations must error so the store retains the previous snapshot")
	}
}

func TestBISErrorsOnTruncatedHeader(t *testing.T) {
	srv := bisServer(t, 200, "FREQ,REF_AREA,UNIT_MEASURE\n")
	b := NewBIS()
	b.URL = srv.URL
	if _, err := b.Fetch(context.Background()); err == nil {
		t.Fatal("a header with fewer columns than the parser indexes must error")
	}
}

func TestBISHistoryLengthFromEnv(t *testing.T) {
	t.Setenv("OPENRATE_BIS_HISTORY", "5")
	if got := NewBIS().URL; !strings.Contains(got, "lastNObservations=5") {
		t.Errorf("OPENRATE_BIS_HISTORY=5 should bound the request, got %q", got)
	}
	// A nonsense value must fall back to the default rather than build a broken URL.
	t.Setenv("OPENRATE_BIS_HISTORY", "not-a-number")
	if got := NewBIS().URL; !strings.Contains(got, "lastNObservations=90") {
		t.Errorf("an invalid OPENRATE_BIS_HISTORY should fall back to 90, got %q", got)
	}
	t.Setenv("OPENRATE_BIS_HISTORY", "-3")
	if got := NewBIS().URL; !strings.Contains(got, "lastNObservations=90") {
		t.Errorf("a negative OPENRATE_BIS_HISTORY should fall back to 90, got %q", got)
	}
}
