package main

// Gates for the hand-written landing page (site/index.html).
//
// site/docs is generated, so it cannot lie about its source — the tests in
// gen_test.go compare it byte for byte. site/index.html is not generated: it is
// written by hand from a capture of the running binary, and the arithmetic
// printed on it was therefore only ever as true as whoever typed it.
//
// It was not true. §02 printed a two-hop cross rate as
//
//	0.060362 × 1.425186 = 0.086027
//
// and claimed the legs and the rate agreed "exactly at six decimal places".
// The engine's invariant is that the legs multiply to the rate at FULL
// precision (fx/precision_test.go pins that, bit for bit). It says
// nothing about the rounded values a reader sees: each of those three numbers
// is rounded once, independently, and independent roundings do not compose. On
// a live snapshot of the default feed set, 27 of 34 two-hop crosses differed in
// the last displayed decimal. The claim was true of the pair on the page and
// false in general — and when it was first noticed, the pair on the page was
// swapped for one that still reconciled, which fixed the example and left the
// defect.
//
// So the page now prints the residual instead of asserting there isn't one, and
// this file re-derives every digit of that arithmetic from the digits printed
// beside it. The page can state a reconciliation only if it actually has one.
//
// Exact decimal arithmetic throughout (math/big.Rat): the printed values are
// decimals, and checking a decimal claim in float64 would reintroduce the very
// class of error being tested.

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// displayDecimals is the precision every rate on the landing page and in the web
// UI is printed to (web/ui.html: fmt(x, 6)).
const displayDecimals = 6

// minReconRows is the coverage floor. The whole point of the figure is that it
// shows a pair that reconciles AND a pair that does not, so a single row cannot
// satisfy it — and a gate that passes because the rows were deleted is the
// failure mode this repo writes gates to avoid.
const minReconRows = 2

// reconRowRE captures one reconciliation row and its whole body. Rows are flat
// (no nested <div>), so a non-greedy match to the closing tag is exact.
var reconRowRE = regexp.MustCompile(`(?s)<div class="recon-row" data-pair="([^"]+)">(.*?)</div>`)

// numRE pulls the text of one <b class="..."> figure inside a row.
func numRE(class string) *regexp.Regexp {
	return regexp.MustCompile(`<b class="` + class + `(?:\s[^"]*)?">([^<]*)</b>`)
}

var (
	legRE   = numRE("leg")
	prodRE  = numRE("prod")
	rateRE  = numRE("rate")
	residRE = numRE("resid")
	// The drawn walk's own per-leg figures, so the picture and the sum below it
	// cannot drift apart. Anchored on the enclosing <span class="m"> because
	// class="r" alone also matches the grade equation in §04.
	walkLegRE = regexp.MustCompile(`<span class="m"><span class="r">([^<]*)</span>`)
)

// forbidden are claims the page must not make again. Each is the exact defect
// this file exists to prevent, not a style preference: every one of them asserts
// that rounded values multiply out, which is false for most pairs.
var forbidden = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`(?i)exactly at (six|6) decimal`),
		"the displayed legs do not multiply to the displayed rate exactly at six decimals on most pairs — print the residual instead"},
	{regexp.MustCompile(`(?i)legs?[^.]{0,40}multipl[a-z]*[^.]{0,40}exactly`),
		"the exact product is the full-precision one; say that, or print the residual"},
}

// normNum strips the separators a typographer might add and returns the bare
// decimal. Thin spaces, plain spaces and commas are all grouping marks here.
func normNum(s string) string {
	r := strings.NewReplacer(",", "", " ", "", " ", "", " ", "", " ", "")
	return strings.TrimSpace(r.Replace(s))
}

// decimalPlaces reports the digits after the point, or -1 if there is no point.
func decimalPlaces(s string) int {
	i := strings.IndexByte(s, '.')
	if i < 0 {
		return -1
	}
	return len(s) - i - 1
}

// parseFigure reads one printed number, insisting it is a plain decimal carried
// to exactly displayDecimals places. A figure printed to five places would make
// the residual unverifiable, and a figure printed to seven would not be what the
// UI shows.
func parseFigure(t *testing.T, pair, what, raw string) *big.Rat {
	t.Helper()
	s := normNum(raw)
	body := strings.TrimLeft(s, "+-−")
	if got := decimalPlaces(body); got != displayDecimals {
		t.Errorf("%s: %s is printed as %q — %d decimal places, want exactly %d (the precision the UI prints)",
			pair, what, raw, got, displayDecimals)
	}
	v, ok := new(big.Rat).SetString(strings.Replace(s, "−", "-", 1))
	if !ok {
		t.Fatalf("%s: %s is printed as %q, which is not a number — this gate cannot check the arithmetic", pair, what, raw)
	}
	return v
}

// fmt6 renders an exact rational at displayDecimals places, the way the page
// prints it.
func fmt6(v *big.Rat) string { return v.FloatString(displayDecimals) }

// roundTo6 rounds an exact rational to displayDecimals places, half away from
// zero — the rule Intl.NumberFormat applies by default and therefore the rule
// every number on this page was produced by.
func roundTo6(v *big.Rat) *big.Rat {
	scale := new(big.Rat).SetInt64(1000000)
	scaled := new(big.Rat).Mul(v, scale)
	num, den := scaled.Num(), scaled.Denom()
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	// |r|*2 >= |den| means the fraction is at or past the half, so step away
	// from zero.
	twice := new(big.Int).Abs(r)
	twice.Lsh(twice, 1)
	if twice.Cmp(new(big.Int).Abs(den)) >= 0 {
		if scaled.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return new(big.Rat).SetFrac(q, big.NewInt(1000000))
}

func landingHTML(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "site", "index.html"))
	if err != nil {
		t.Fatalf("read site/index.html: %v — the landing gates verified NOTHING", err)
	}
	return string(raw)
}

// TestLandingCrossRateArithmetic re-derives §02's reconciliation rows from the
// digits printed in them. For each row: the legs, multiplied and rounded to the
// displayed precision, must be the product printed; and the residual printed
// must be the actual difference between that product and the rate printed,
// sign included.
func TestLandingCrossRateArithmetic(t *testing.T) {
	html := landingHTML(t)

	rows := reconRowRE.FindAllStringSubmatch(html, -1)
	if len(rows) < minReconRows {
		t.Fatalf("site/index.html has %d .recon-row blocks (floor %d) — §02's reconciliation figure is gone or "+
			"renamed, and this gate checked nothing. Restore it, or delete this test deliberately.", len(rows), minReconRows)
	}

	nonZero := 0
	for _, row := range rows {
		pair, body := row[1], row[2]

		legs := legRE.FindAllStringSubmatch(body, -1)
		if len(legs) < 2 {
			t.Errorf("%s: %d <b class=\"leg\"> figures — a cross rate needs at least two", pair, len(legs))
			continue
		}
		prodM := prodRE.FindStringSubmatch(body)
		rateM := rateRE.FindStringSubmatch(body)
		residM := residRE.FindStringSubmatch(body)
		if prodM == nil || rateM == nil || residM == nil {
			t.Errorf("%s: row is missing one of prod/rate/resid — the reader is shown arithmetic nobody checked", pair)
			continue
		}

		product := new(big.Rat).SetInt64(1)
		for i, l := range legs {
			product.Mul(product, parseFigure(t, pair, fmt.Sprintf("leg %d", i+1), l[1]))
		}
		wantProd := roundTo6(product)

		gotProd := parseFigure(t, pair, "product", prodM[1])
		gotRate := parseFigure(t, pair, "rate", rateM[1])
		gotResid := parseFigure(t, pair, "residual", residM[1])

		if gotProd.Cmp(wantProd) != 0 {
			t.Errorf("%s: the legs as printed multiply to %s, but the page prints the product as %s — "+
				"the reader cannot reproduce this figure", pair, fmt6(wantProd), fmt6(gotProd))
		}
		wantResid := new(big.Rat).Sub(gotProd, gotRate)
		if gotResid.Cmp(wantResid) != 0 {
			t.Errorf("%s: product %s minus rate %s is %s, but the page prints the residual as %s — "+
				"the one number on this figure whose job is to be honest is wrong",
				pair, fmt6(gotProd), fmt6(gotRate), fmt6(wantResid), fmt6(gotResid))
		}
		if wantResid.Sign() != 0 {
			nonZero++
			// A non-zero residual must carry its sign; "0.002282" where the true
			// value is +0.002282 reads as a magnitude and hides the direction.
			s := normNum(residM[1])
			if !strings.HasPrefix(s, "+") && !strings.HasPrefix(s, "-") && !strings.HasPrefix(s, "−") {
				t.Errorf("%s: residual %q is non-zero but printed without a sign", pair, residM[1])
			}
		}
		t.Logf("%s: %s = %s · rate %s · residual %s", pair, legsJoin(legs), fmt6(gotProd), fmt6(gotRate), fmt6(gotResid))
	}

	// The anti-flattery floor. A figure showing only pairs whose rounding lands
	// cleanly is how the false claim survived review the first time: it is true
	// of everything on the page and false of the product. At least one row must
	// be a pair where the displayed numbers do NOT reconcile.
	if nonZero == 0 {
		t.Errorf("every reconciliation row on the landing has a zero residual. That is the cherry-picked figure "+
			"this gate exists to prevent: rounding each leg and the rate independently makes them disagree on "+
			"most real pairs, and a page that only ever shows the pairs where it doesn't is claiming a property "+
			"the engine does not have. Keep at least one row (of %d) with a real residual.", len(rows))
	}
}

func legsJoin(legs [][]string) string {
	var parts []string
	for _, l := range legs {
		parts = append(parts, normNum(l[1]))
	}
	return strings.Join(parts, " × ")
}

// TestLandingWalkMatchesItsArithmetic keeps the drawn two-hop walk and the sum
// underneath it in step. The picture prints each leg; the first reconciliation
// row multiplies them. If someone recaptures the app and updates one without
// the other, the figure quietly starts illustrating a different calculation
// from the one it shows the answer to.
func TestLandingWalkMatchesItsArithmetic(t *testing.T) {
	html := landingHTML(t)

	drawn := walkLegRE.FindAllStringSubmatch(html, -1)
	if len(drawn) < 2 {
		t.Fatalf("found %d drawn walk legs in site/index.html (want at least 2) — the walk figure is gone or "+
			"renamed and this gate checked nothing", len(drawn))
	}
	rows := reconRowRE.FindAllStringSubmatch(html, -1)
	if len(rows) == 0 {
		t.Fatal("no .recon-row blocks — nothing to compare the walk against")
	}
	summed := legRE.FindAllStringSubmatch(rows[0][2], -1)
	if len(summed) != len(drawn) {
		t.Fatalf("the walk draws %d legs but its sum multiplies %d — the picture and the arithmetic are not the same calculation",
			len(drawn), len(summed))
	}
	for i := range drawn {
		if a, b := normNum(drawn[i][1]), normNum(summed[i][1]); a != b {
			t.Errorf("walk leg %d is drawn as %s but multiplied as %s — the figure contradicts itself", i+1, a, b)
		}
	}
	t.Logf("walk: %d legs drawn and multiplied identically", len(drawn))
}

// TestLandingMakesNoExactnessClaim is the regression guard proper. The defect
// was not a wrong digit — every digit was right — it was a sentence asserting a
// property of rounded values that only holds by luck. Bring the sentence back
// and this fails, whatever the digits say.
func TestLandingMakesNoExactnessClaim(t *testing.T) {
	html := landingHTML(t)
	for _, f := range forbidden {
		if m := f.re.FindString(html); m != "" {
			t.Errorf("site/index.html says %q — %s", strings.TrimSpace(m), f.why)
		}
	}
	// And the page must still SAY the true rule somewhere; deleting the claim
	// without replacing it leaves a reader multiplying two printed numbers and
	// getting a third one that does not match, with no explanation.
	if !strings.Contains(html, "full precision") {
		t.Error("site/index.html no longer explains that the cross rate is the product of the legs at FULL " +
			"precision, rounded once for display. Removing the false claim is only half the fix: without the " +
			"true rule, the residual printed in §02 has nothing to attach to.")
	}
}
