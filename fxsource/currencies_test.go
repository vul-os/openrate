package fxsource

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// openrate keeps its own currency-code table (fiatAllow/cryptoAllow in fiat.go).
// It is deliberately self-contained — no other repo's table is imported — which
// means nothing outside this file notices when it drifts from ISO 4217.
//
// These tests pin the table by checksum. They do not (and cannot) prove the
// table matches ISO 4217; what they do is make any change to it *deliberate*:
// an edit fails the build with the old and new digests, and the maintainer must
// re-verify against the authoritative list before re-pinning.
//
// Authoritative list: ISO 4217, maintained by SIX Group on behalf of the ISO
// 4217 Maintenance Agency — https://www.six-group.com/en/products-services/financial-information/data-standards.html
// (`list_one.xml` is the machine-readable form). ISO 4217 also assigns each code
// a minor-unit exponent; openrate does NOT carry the exponent column, because
// nothing in the engine formats minor units — rates are transported as decimal
// numbers and presentation rounding is the consumer's concern. Do not add an
// exponent column here speculatively; add it with the code that needs it.

// Pinned digests: sha256 over the sorted, comma-joined code list.
//
// To re-pin after a *verified* change, run:
//
//	go test ./fxsource/ -run TestCurrencyTableChecksum -v
//
// and copy the reported digest.
const (
	fiatAllowSHA256   = "673e5924647c304396620d17258c27b9dd6bfce332cb3538fa35f807e009376a"
	cryptoAllowSHA256 = "239960d829bd09ceed6e70721d9577e1e2c7e84e31704de8c3a6db09b777e1ad"

	// Counts are pinned separately so a table that is emptied or truncated fails
	// loudly on its own terms, not only as an opaque digest mismatch.
	fiatAllowCount   = 39
	cryptoAllowCount = 4
)

// sortedCodes returns the table's codes in a stable, canonical order.
func sortedCodes(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func digest(codes []string) string {
	sum := sha256.Sum256([]byte(strings.Join(codes, ",")))
	return hex.EncodeToString(sum[:])
}

func TestCurrencyTableChecksum(t *testing.T) {
	for _, tc := range []struct {
		name      string
		table     map[string]bool
		wantSHA   string
		wantCount int
	}{
		{"fiatAllow", fiatAllow, fiatAllowSHA256, fiatAllowCount},
		{"cryptoAllow", cryptoAllow, cryptoAllowSHA256, cryptoAllowCount},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codes := sortedCodes(tc.table)
			if len(codes) != tc.wantCount {
				t.Errorf("%s has %d codes, pinned at %d", tc.name, len(codes), tc.wantCount)
			}
			got := digest(codes)
			t.Logf("%s n=%d sha256=%s", tc.name, len(codes), got)
			if got != tc.wantSHA {
				t.Errorf(`%s drifted.
  pinned sha256: %s
  actual sha256: %s
  actual codes : %s

The currency table changed. Before re-pinning, verify every added or removed
code against the authoritative ISO 4217 list (SIX Group, list_one.xml) — that a
code parses as three letters does not make it a currency. Then update
%sSHA256 and %sCount in this file.`,
					tc.name, tc.wantSHA, got, strings.Join(codes, ","), tc.name, tc.name)
			}
		})
	}
}

// iso4217RE is the ISO 4217 alphabetic-code shape: exactly three uppercase A–Z.
// It applies to fiatAllow only.
var iso4217RE = regexp.MustCompile(`^[A-Z]{3}$`)

// tickerRE is the shape of a crypto asset symbol. These are venue tickers, not
// ISO 4217 codes — ISO 4217 has no entry for BTC, ETH, USDT or USDC (the
// unofficial "XBT" convention is not in the standard either), and stablecoin
// tickers are routinely four characters. They are held to a ticker shape rather
// than the ISO one precisely so nobody mistakes them for standardised codes.
var tickerRE = regexp.MustCompile(`^[A-Z0-9]{2,5}$`)

func TestCurrencyCodesAreWellFormed(t *testing.T) {
	checked := 0
	for _, tc := range []struct {
		name  string
		table map[string]bool
		re    *regexp.Regexp
		shape string
	}{
		{"fiatAllow", fiatAllow, iso4217RE, "an ISO 4217 alphabetic code (exactly three uppercase A–Z)"},
		{"cryptoAllow", cryptoAllow, tickerRE, "a crypto ticker (2–5 uppercase alphanumerics)"},
	} {
		for _, c := range sortedCodes(tc.table) {
			checked++
			if !tc.re.MatchString(c) {
				t.Errorf("%s: %q is not %s", tc.name, c, tc.shape)
			}
			if !tc.table[c] {
				t.Errorf("%s: %q maps to false; the tables are membership sets and every entry must be true", tc.name, c)
			}
		}
	}
	if want := fiatAllowCount + cryptoAllowCount; checked != want {
		t.Fatalf("checked %d codes, expected %d — the loop is not covering both tables", checked, want)
	}
}

// TestCryptoCodesAreNotClaimedAsISO4217 guards the boundary the two shapes
// encode: nothing in cryptoAllow may be presented as an ISO 4217 currency.
func TestCryptoCodesAreNotClaimedAsISO4217(t *testing.T) {
	// Codes ISO 4217 actually reserves for non-currency use. If a crypto ticker
	// ever collides with the standard's namespace, that is a decision to make
	// explicitly, not to discover in production.
	reserved := map[string]bool{"XXX": true, "XAU": true, "XAG": true, "XPT": true, "XPD": true, "XDR": true, "XTS": true}
	for _, c := range sortedCodes(cryptoAllow) {
		if reserved[c] {
			t.Errorf("crypto ticker %q collides with an ISO 4217 reserved code", c)
		}
		if fiatAllow[c] {
			t.Errorf("crypto ticker %q is also listed as an ISO 4217 fiat currency", c)
		}
	}
}

func TestFiatAndCryptoTablesAreDisjoint(t *testing.T) {
	for _, c := range sortedCodes(cryptoAllow) {
		if fiatAllow[c] {
			t.Errorf("%q appears in both fiatAllow and cryptoAllow; a code must have exactly one class", c)
		}
	}
}

// TestAllowedIsExactlyTheUnion locks the only consumer of the tables to them, so
// a code cannot be admitted (or dropped) by editing `allowed` alone.
func TestAllowedIsExactlyTheUnion(t *testing.T) {
	for _, c := range append(sortedCodes(fiatAllow), sortedCodes(cryptoAllow)...) {
		if !allowed(c) {
			t.Errorf("allowed(%q) = false, but %q is in a table", c, c)
		}
	}
	for _, c := range []string{"XXX", "ZZZ", "AAA", "", "usd", "US", "USDD"} {
		if allowed(c) {
			t.Errorf("allowed(%q) = true, but %q is in neither table", c, c)
		}
	}
}

// TestUIMetadataCoversEngineCurrencies keeps the repo's *second* currency table
// honest. web/ui.html carries a CCY_NAMES display-name table keyed by the same
// codes; when the engine admits a currency the UI has never heard of, the
// dropdown silently degrades to a bare code. The two lists are maintained by
// hand in different languages, so nothing but a test couples them.
func TestUIMetadataCoversEngineCurrencies(t *testing.T) {
	path := filepath.Join("..", "serve", "web", "ui.html")
	data, err := os.ReadFile(path)
	if err != nil {
		// Not a skip: the file is committed in this repo. If it moved, this test
		// must be updated, not silently disabled.
		t.Fatalf("cannot read the UI currency table at %s: %v", path, err)
	}

	// Scope the search to the CCY_NAMES object literal so a stray ISO-code-shaped
	// string elsewhere in the page can't be miscounted as display metadata.
	tableRE := regexp.MustCompile(`(?s)CCY_NAMES\s*=\s*\{(.*?)\};`)
	tm := tableRE.FindStringSubmatch(string(data))
	if tm == nil {
		t.Fatalf("could not find a CCY_NAMES = {...}; object literal in %s — the UI currency table moved or was renamed", path)
	}

	// Entries look like:  USD:"US Dollar",EUR:"Euro",...
	// The width is 2–5 because stablecoin tickers (USDT, USDC) are not 3 chars.
	keyRE := regexp.MustCompile(`([A-Z0-9]{2,5}):"`)
	matches := keyRE.FindAllStringSubmatch(tm[1], -1)
	ui := map[string]bool{}
	for _, m := range matches {
		ui[m[1]] = true
	}

	engine := append(sortedCodes(fiatAllow), sortedCodes(cryptoAllow)...)
	// Assert the parser actually saw a plausible table before trusting a "no
	// missing codes" result — otherwise a regex that stops matching would make
	// this test pass by finding nothing.
	if len(ui) < len(engine) {
		t.Fatalf("parsed only %d currency keys out of %s but the engine admits %d; "+
			"the parser no longer matches the file's shape, so a missing-metadata result would be meaningless",
			len(ui), path, len(engine))
	}
	t.Logf("UI table has %d entries, engine admits %d", len(ui), len(engine))
	var missing []string
	for _, c := range engine {
		if !ui[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		t.Errorf("web/ui.html is missing display metadata for %d engine currency/currencies: %s\n"+
			"The converter will render these as bare codes. Add them to CCY_NAMES or remove them from fiat.go.",
			len(missing), strings.Join(missing, ", "))
	}
}
