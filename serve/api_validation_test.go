package serve_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vul-os/openrate"
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

// /rates had the same SHAPE of hole as the convert bug — Rebase yields nothing
// for a base it does not know, so ?base=NOTACCY answered 200 with an empty book,
// which is also exactly what a server holding no rates answers — but unlike the
// convert bug it was a published contract: five SDKs documented the difference
// from the library and two example programs demonstrated it on purpose.
//
// It is now a 404 on all three surfaces. `base` is the pivot of the response,
// not a filter over it: every entry means "1 base = rate units of X", so an
// unknown base does not describe an empty table, it makes the document's own
// definition false while echoing the invented code back as if it were real. The
// 200 was also indistinguishable from a cold start, which answers 200 with an
// empty book for every base — the exact ambiguity /readyz was added to resolve.
//
// This test pins the aligned contract, and the next one pins the case that must
// NOT be swept up with it.
func TestRatesUnknownBaseIsRefusedLikeTheLibrary(t *testing.T) {
	engine := populatedEngine(t, testEdges, 3)
	srv := apiServer(t, engine)

	code, body := getBody(t, srv.URL, "/api/v1/rates?base=NOTACCY")
	if code != http.StatusNotFound {
		t.Errorf("/rates?base=NOTACCY: status = %d, want 404 — an unknown base is refused here exactly as "+
			"Engine.Rates and the C ABI refuse it, and as /convert already refuses the same code: %s",
			code, truncate([]byte(body)))
	}
	// The body is the sentinel's own text, so a client can match one string
	// across the HTTP API, the library and the ABI.
	var refusal struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &refusal); err != nil {
		t.Fatalf("/rates?base=NOTACCY: decode %q: %v", body, err)
	}
	if refusal.Error != openrate.ErrUnknownBase.Error() {
		t.Errorf("/rates?base=NOTACCY: error = %q, want ErrUnknownBase's own text %q",
			refusal.Error, openrate.ErrUnknownBase.Error())
	}
	// And nothing that looks like an answer came with it: no echoed base, no
	// book. The old response carried both.
	if strings.Contains(body, `"rates"`) || strings.Contains(body, `"base"`) {
		t.Errorf("/rates?base=NOTACCY: the refusal still carries the shape of an answer: %s", truncate([]byte(body)))
	}

	// THE alignment assertion: for the very same engine, the HTTP layer and the
	// library must agree on every base — one refuses exactly when the other
	// does. This is what the three-surface disagreement cost, so it is asserted
	// rather than described.
	bases := []string{"", "USD", "usd", "ZAR", "EUR", "NOTACCY", "notaccy", "ZZZ", " zar "}
	agreed := 0
	for _, b := range bases {
		q := ""
		if b != "" {
			q = "?base=" + url.QueryEscape(b)
		}
		code, body := getBody(t, srv.URL, "/api/v1/rates"+q)
		_, libErr := engine.Rates(b)
		httpRefused := code != http.StatusOK
		libRefused := libErr != nil
		if httpRefused != libRefused {
			t.Errorf("base %q: HTTP answered %d and Engine.Rates returned %v — the two surfaces disagree "+
				"about whether this base exists: %s", b, code, libErr, truncate([]byte(body)))
			continue
		}
		agreed++
		if !httpRefused {
			var out struct {
				Rates map[string]any `json:"rates"`
			}
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				t.Fatalf("/rates%s: decode: %v", q, err)
			}
			// A known base has to carry a real book, or "they agree" would be
			// satisfied by an endpoint that answers 200 with nothing.
			if len(out.Rates) == 0 {
				t.Fatalf("/rates%s: served an empty book for a base both surfaces accept", q)
			}
		}
	}
	if agreed != len(bases) || agreed != 9 {
		t.Fatalf("compared %d of 9 bases across the two surfaces", agreed)
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
