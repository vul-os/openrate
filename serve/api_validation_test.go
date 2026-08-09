package serve_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Input validation on the read endpoints. Every case here is a request that used
// to come back 200 with a made-up answer in it.

// getBody drives the real handler and returns the status and body.
func getBody(t *testing.T, srv string, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(srv + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp.StatusCode, string(b)
}

// The reviewer's case: /convert?from=NOTACCY&to=NOTACCY answered
// 200 {"rate":{"rate":1,...,"quality":{"grade":"B"}}}. Snapshot.Lookup returns
// the identity for from == to whatever the string is, so the endpoint invented a
// graded exchange rate for a currency that does not exist. The library and the C
// ABI both consult Snapshot.Has; the HTTP layer did not.
func TestConvertUnknownCurrencyCodeIsRejected(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	cases := []string{
		"?from=NOTACCY&to=NOTACCY", // both sides unknown AND equal — the identity trap
		"?from=NOTACCY&to=USD",
		"?from=USD&to=NOTACCY",
		"?from=notaccy&to=notaccy", // case-folding must not open a side door
		"?from=ZZZ&to=ZZZ&amount=100",
	}
	for _, q := range cases {
		code, body := getBody(t, srv.URL, "/api/v1/convert"+q)
		if code != http.StatusNotFound {
			t.Errorf("/convert%s: status = %d, want 404 — body: %s", q, code, truncate([]byte(body)))
		}
		if !strings.Contains(body, "unknown or unreachable currency pair") {
			t.Errorf("/convert%s: body = %s, want the ErrUnknownPair text", q, truncate([]byte(body)))
		}
		if strings.Contains(body, `"grade"`) {
			t.Errorf("/convert%s: a refusal must not carry a quality grade: %s", q, truncate([]byte(body)))
		}
	}
	if len(cases) != 5 {
		t.Fatalf("the case table lost entries: %d, want 5", len(cases))
	}
}

// The other half of the same fix: the identity conversion of a currency the
// snapshot DOES know is a true statement and must keep working, rate 1 and all.
func TestConvertIdentityForAKnownCurrencyStillWorks(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	for _, ccy := range []string{"USD", "ZAR", "EUR", "usd"} {
		code, body := getBody(t, srv.URL, "/api/v1/convert?from="+ccy+"&to="+ccy+"&amount=42")
		if code != http.StatusOK {
			t.Fatalf("%s->%s: status = %d, want 200 — the unknown-code guard is refusing known currencies: %s",
				ccy, ccy, code, truncate([]byte(body)))
		}
		var out struct {
			Amount float64 `json:"amount"`
			Result float64 `json:"result"`
			Rate   struct {
				Rate float64 `json:"rate"`
			} `json:"rate"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("%s: decode: %v", ccy, err)
		}
		if out.Rate.Rate != 1 || out.Result != 42 || out.Amount != 42 {
			t.Errorf("%s->%s 42: rate %v result %v amount %v, want 1 / 42 / 42",
				ccy, ccy, out.Rate.Rate, out.Result, out.Amount)
		}
	}
}

// An amount literal too large for a float64 parses to ±Inf AND returns
// strconv.ErrRange. The endpoint tested only `err == nil`, so it discarded both
// and converted 1.0 — a 200 describing a different amount than the caller asked
// about, which is worse than the 400 the same value gets when written as "Inf".
func TestConvertOutOfRangeAmountIsRejected(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	cases := []string{
		"1e999",
		"-1e999",
		"1e310",
		strings.Repeat("9", 400), // 400 digits: out of range without an exponent
	}
	for _, amt := range cases {
		code, body := getBody(t, srv.URL, "/api/v1/convert?from=USD&to=ZAR&amount="+url.QueryEscape(amt))
		if code != http.StatusBadRequest {
			t.Errorf("amount=%.20q: status = %d, want 400 — the request silently converted a different amount: %s",
				amt, code, truncate([]byte(body)))
		}
		if !strings.Contains(body, "invalid amount") {
			t.Errorf("amount=%.20q: body = %s, want the ErrInvalidAmount text", amt, truncate([]byte(body)))
		}
	}
	if len(cases) != 4 {
		t.Fatalf("the case table lost entries: %d, want 4", len(cases))
	}
}

// The three amount cases the old code collapsed into one, pinned apart: absent
// and unparseable keep the documented default of 1; out-of-range is a 400.
func TestConvertAmountCasesAreDistinct(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	cases := []struct {
		name       string
		query      string
		wantStatus int
		wantAmount float64 // only checked on a 200
	}{
		{"absent", "", http.StatusOK, 1},
		{"empty", "&amount=", http.StatusOK, 1},
		{"unparseable", "&amount=notanumber", http.StatusOK, 1},
		{"thousands separator", "&amount=1,000", http.StatusOK, 1},
		{"ordinary", "&amount=100", http.StatusOK, 100},
		{"zero", "&amount=0", http.StatusOK, 0},
		{"negative", "&amount=-5", http.StatusOK, -5},
		{"underflows to zero", "&amount=1e-999", http.StatusOK, 0},
		{"out of range", "&amount=1e999", http.StatusBadRequest, 0},
		{"non-finite", "&amount=Inf", http.StatusBadRequest, 0},
		{"negative non-finite", "&amount=-Inf", http.StatusBadRequest, 0},
		{"not a number", "&amount=NaN", http.StatusBadRequest, 0},
	}
	ran := 0
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := getBody(t, srv.URL, "/api/v1/convert?from=USD&to=ZAR"+tc.query)
			if code != tc.wantStatus {
				t.Fatalf("status = %d, want %d — body: %s", code, tc.wantStatus, truncate([]byte(body)))
			}
			if code != http.StatusOK {
				return
			}
			var out struct {
				Amount float64 `json:"amount"`
				Result float64 `json:"result"`
			}
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if out.Amount != tc.wantAmount {
				t.Errorf("amount = %v, want %v", out.Amount, tc.wantAmount)
			}
			if want := tc.wantAmount * 18.50; out.Result != want {
				t.Errorf("result = %v, want %v", out.Result, want)
			}
		})
		ran++
	}
	if ran != len(cases) || ran != 12 {
		t.Fatalf("ran %d cases, want all 12", ran)
	}
}

// /rates has the same SHAPE of hole as the convert bug — Rebase yields nothing
// for a base it does not know, so ?base=NOTACCY answers 200 with an empty book,
// which is also exactly what a server holding no rates answers — but unlike the
// convert bug it is a published contract. sdks/go, sdks/python and sdks/deno
// document the difference from the library in their READMEs, two of their
// example programs demonstrate it on purpose (sdks/go's sidecar example FAILS if
// the request returns anything but 200), and ffi/abi cites it as the behaviour
// it deliberately departs from. Aligning it with Engine.Rates means changing
// those in the same commit, so it is an API-contract decision rather than a fix
// to make inside serve.
//
// This test pins the shipped behaviour in BOTH directions so the decision, when
// it is taken, is taken deliberately.
func TestRatesUnknownBaseKeepsItsDocumentedShape(t *testing.T) {
	srv := apiServer(t, populatedEngine(t, testEdges, 3))

	code, body := getBody(t, srv.URL, "/api/v1/rates?base=NOTACCY")
	if code != http.StatusOK {
		t.Errorf("/rates?base=NOTACCY: status = %d, want 200 — this is the documented cross-SDK contract; "+
			"changing it means changing sdks/{go,python,deno} and ffi/abi in the same commit: %s",
			code, truncate([]byte(body)))
	}
	var unknown struct {
		Base  string         `json:"base"`
		Rates map[string]any `json:"rates"`
	}
	if err := json.Unmarshal([]byte(body), &unknown); err != nil {
		t.Fatalf("/rates?base=NOTACCY: decode: %v", err)
	}
	if unknown.Base != "NOTACCY" || len(unknown.Rates) != 0 {
		t.Errorf("/rates?base=NOTACCY: base %q with %d pairs, want the echoed base and an empty book",
			unknown.Base, len(unknown.Rates))
	}

	// Known bases, including the default and a lower-case spelling, carry a real
	// book — so the empty one above is genuinely the unknown-base answer and not
	// a broken fixture.
	for _, q := range []string{"", "?base=USD", "?base=usd", "?base=ZAR"} {
		code, body := getBody(t, srv.URL, "/api/v1/rates"+q)
		if code != http.StatusOK {
			t.Fatalf("/rates%s: status = %d, want 200 — the guard is refusing a known base: %s",
				q, code, truncate([]byte(body)))
		}
		var out struct {
			Rates map[string]any `json:"rates"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("/rates%s: decode: %v", q, err)
		}
		if len(out.Rates) == 0 {
			t.Fatalf("/rates%s: served an empty book for a known base", q)
		}
	}
}

// A snapshot with no currencies is "nothing yet", not a bad request: that is a
// readiness question and /readyz is the endpoint that answers it. The guard must
// not turn a cold start into a 404 for every base.
func TestRatesOnAColdEngineStillAnswers(t *testing.T) {
	srv := httptest.NewServer(emptyServer(t))
	defer srv.Close()

	for _, q := range []string{"", "?base=USD", "?base=NOTACCY"} {
		code, body := getBody(t, srv.URL, "/api/v1/rates"+q)
		if code != http.StatusOK {
			t.Errorf("cold engine /rates%s: status = %d, want 200 — body: %s", q, code, truncate([]byte(body)))
		}
	}
}
