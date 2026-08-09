package api

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/vul-os/openrate/fx"
)

// A rate that is not a positive finite number must never reach the wire.
//
// api.writeJSON sets the status and Content-Type before streaming, and discards
// the encoder's error (`_ = enc.Encode(v)`). So if a NaN or +Inf reaches the
// encoder, the consumer gets 200 + `Content-Type: application/json` + a
// truncated or empty body, with nothing anywhere to indicate failure. A client
// such as SlipScan, which writes its own HTTP client against this API, would
// treat that as a successful poll.
//
// The engine already rejects the amount query param when it is non-finite; this
// covers the other half — a non-finite value arriving from a *source*, which is
// reachable because every FX source parses wire strings with strconv.ParseFloat
// and guards only with `rate <= 0` (a test NaN fails that comparison).

// poisonEdges mixes good pairs with rates no arithmetic can represent.
func poisonEdges(bad float64) []fx.Edge {
	return []fx.Edge{
		{From: "USD", To: "ZAR", Rate: 18.50, Source: "sarb", Time: apiTestTime},
		{From: "USD", To: "EUR", Rate: 0.92, Source: "ecb", Time: apiTestTime},
		{From: "USD", To: "XXX", Rate: bad, Source: "junk", Time: apiTestTime},
	}
}

func TestRatesResponseIsAlwaysCompleteJSON(t *testing.T) {
	for name, bad := range map[string]float64{
		"NaN":  math.NaN(),
		"+Inf": math.Inf(1),
		"-Inf": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			st, cancel := populatedStore(t, poisonEdges(bad), 3)
			defer cancel()
			srv := apiServer(t, st)
			defer srv.Close()

			for _, path := range []string{
				"/api/v1/rates?base=USD",
				"/api/v1/rates?base=ZAR",
				"/api/v1/convert?from=USD&to=ZAR&amount=100",
				"/api/v1/meta",
			} {
				resp, err := http.Get(srv.URL + path)
				if err != nil {
					t.Fatalf("GET %s: %v", path, err)
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					t.Fatalf("GET %s: status %d, want 200", path, resp.StatusCode)
				}
				if len(body) == 0 {
					t.Fatalf("GET %s: 200 with an EMPTY body — the encoder aborted and the error was discarded", path)
				}
				// The decisive check: a truncated stream is not valid JSON.
				var v any
				if err := json.Unmarshal(body, &v); err != nil {
					t.Fatalf("GET %s: 200 with a body that is not valid JSON (%v): %s", path, err, truncate(body))
				}
				for _, tok := range []string{"NaN", "Infinity", "+Inf", "-Inf"} {
					if strings.Contains(string(body), tok) {
						t.Fatalf("GET %s: response contains the non-finite token %q: %s", path, tok, truncate(body))
					}
				}
			}
		})
	}
}

// TestPoisonedPairIsAbsentNotCorrupt asserts the *shape* of the degradation: the
// unrepresentable pair is simply not offered, while every pair that is offered
// carries a usable number. Silently omitting a pair is correct; serving a
// corrupt one is not.
func TestPoisonedPairIsAbsentNotCorrupt(t *testing.T) {
	st, cancel := populatedStore(t, poisonEdges(math.NaN()), 3)
	defer cancel()
	srv := apiServer(t, st)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/rates?base=USD")
	if err != nil {
		t.Fatalf("GET rates: %v", err)
	}
	defer resp.Body.Close()

	var out struct {
		Base  string `json:"base"`
		Rates map[string]struct {
			Rate float64 `json:"rate"`
			Legs []struct {
				Rate float64 `json:"rate"`
			} `json:"legs"`
			Quotes []struct {
				Rate float64 `json:"rate"`
			} `json:"quotes"`
		} `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if _, ok := out.Rates["XXX"]; ok {
		t.Fatal("the pair carrying a NaN rate must be absent, not served")
	}
	// The good pairs must survive: one bad source cannot take the API down.
	for _, want := range []string{"ZAR", "EUR"} {
		if _, ok := out.Rates[want]; !ok {
			t.Fatalf("USD->%s must still be served alongside a poisoned edge; got %v", want, keys(out.Rates))
		}
	}
	if n := len(out.Rates); n != 2 {
		t.Fatalf("expected exactly 2 served pairs (ZAR, EUR), got %d: %v", n, keys(out.Rates))
	}
	for ccy, r := range out.Rates {
		if r.Rate <= 0 || math.IsInf(r.Rate, 0) || math.IsNaN(r.Rate) {
			t.Fatalf("USD->%s served an unusable rate %v", ccy, r.Rate)
		}
		for i, l := range r.Legs {
			if l.Rate <= 0 || math.IsInf(l.Rate, 0) || math.IsNaN(l.Rate) {
				t.Fatalf("USD->%s leg %d served an unusable rate %v", ccy, i, l.Rate)
			}
		}
		for i, q := range r.Quotes {
			if q.Rate <= 0 || math.IsInf(q.Rate, 0) || math.IsNaN(q.Rate) {
				t.Fatalf("USD->%s quote %d served an unusable rate %v", ccy, i, q.Rate)
			}
		}
	}
}

// TestConvertResultIsFinite covers the multiplication the convert endpoint does
// on top of the rate (amount * rate), which is the value consumers actually use.
func TestConvertResultIsFinite(t *testing.T) {
	st, cancel := populatedStore(t, poisonEdges(math.NaN()), 3)
	defer cancel()
	srv := apiServer(t, st)
	defer srv.Close()

	// math.MaxFloat64 is finite and parses cleanly, so the non-finite-amount
	// guard lets it through — but amount*rate overflows to +Inf. That must be
	// refused explicitly, not streamed into a body the encoder cannot finish.
	resp, err := http.Get(srv.URL + "/api/v1/convert?from=USD&to=ZAR&amount=1.7976931348623157e308")
	if err != nil {
		t.Fatalf("GET convert: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if len(body) == 0 {
		t.Fatal("convert returned 200 with an empty body: amount*rate overflowed and the encoder error was discarded")
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an amount whose product overflows must be a 400, got %d: %s", resp.StatusCode, truncate(body))
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("the 400 body must be valid JSON (%v): %s", err, truncate(body))
	}

	// A normal amount on the same pair must still succeed — the guard must not
	// have broken ordinary conversion.
	resp, err = http.Get(srv.URL + "/api/v1/convert?from=USD&to=ZAR&amount=100")
	if err != nil {
		t.Fatalf("GET convert: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a normal amount must still convert, got %d: %s", resp.StatusCode, truncate(body))
	}
	var out struct {
		Result float64 `json:"result"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if want := 100 * 18.50; out.Result != want {
		t.Fatalf("result = %v, want %v", out.Result, want)
	}
}

func keys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func truncate(b []byte) string {
	const max = 400
	if len(b) > max {
		return string(b[:max]) + "…"
	}
	return string(b)
}
