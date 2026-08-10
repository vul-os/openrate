package abi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vul-os/openrate"
)

func mustNew(t *testing.T, cfg string) uint64 {
	t.Helper()
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%s): %v", cfg, err)
	}
	if h == 0 {
		t.Fatal("New returned handle 0, which is the failure value")
	}
	return h
}

// engineFor reaches the object behind a handle. Only a test in this package can
// do that, which is the point: a host gets an integer and nothing else.
func engineFor(t *testing.T, h uint64) *engineObj {
	t.Helper()
	obj, err := lookup(h)
	if err != nil {
		t.Fatalf("lookup(%d): %v", h, err)
	}
	eo, ok := obj.(*engineObj)
	if !ok {
		t.Fatalf("handle %d is a %s, not an engine", h, obj.kindName())
	}
	return eo
}

func TestHandlesAreOpaqueIntegersFromARegistry(t *testing.T) {
	before := OpenHandles()

	a := mustNew(t, `{"base":"ZAR","quiet":true}`)
	b := mustNew(t, `{"base":"USD","quiet":true}`)
	if a == b {
		t.Fatal("two engines share a handle")
	}
	if OpenHandles() != before+2 {
		t.Fatalf("registry holds %d handles, want %d", OpenHandles(), before+2)
	}

	// Two engines, two independent defaults: the registry is not handing out
	// one shared object under different numbers.
	if got := defaultBase(t, a); got != "ZAR" {
		t.Errorf("engine a default base %q, want ZAR", got)
	}
	if got := defaultBase(t, b); got != "USD" {
		t.Errorf("engine b default base %q, want USD", got)
	}

	Close(a)
	Close(b)
	if OpenHandles() != before {
		t.Fatalf("registry holds %d handles after closing both, want %d", OpenHandles(), before)
	}
}

// TestUseAfterCloseIsACleanError is the reason handles are integers. A host
// language with a garbage collector will free things in an order nobody
// planned; the answer to that must be a string, not a signal.
func TestUseAfterCloseIsACleanError(t *testing.T) {
	h := mustNew(t, `{"quiet":true}`)
	Close(h)

	if _, err := Call(h, "meta", `{}`); err == nil {
		t.Fatal("calling a closed handle succeeded")
	} else if !strings.Contains(err.Error(), "not open") {
		t.Errorf("error for a closed handle is %q; it should say the handle is not open", err)
	}

	// A double close is a mistake a host will make. It must not be fatal.
	Close(h)
	Close(h)

	// So is an invented handle.
	if _, err := Call(99999, "meta", `{}`); err == nil {
		t.Fatal("calling a handle that was never issued succeeded")
	}
	if _, err := NewRefresher(99999, `{}`); err == nil {
		t.Fatal("building a refresher over a handle that was never issued succeeded")
	}
}

func TestUnknownMethodNamesTheOnesThatExist(t *testing.T) {
	h := mustNew(t, `{"quiet":true}`)
	defer Close(h)

	_, err := Call(h, "covnert", `{}`)
	if err == nil {
		t.Fatal("a misspelled method succeeded")
	}
	for _, want := range []string{"convert", "rates", "meta", "load"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the unknown-method error %q does not list %q; a caller cannot fix a typo "+
				"they cannot see the alternatives to", err, want)
		}
	}
}

// TestAnEngineHandleIsNotARefresherHandle pins the split at the ABI. The two
// kinds answer disjoint method sets, and asking one for the other's methods is
// an error that says which kind you actually passed.
func TestAnEngineHandleIsNotARefresherHandle(t *testing.T) {
	eh := mustNew(t, `{"quiet":true}`)
	defer Close(eh)
	rh, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}
	if rh == eh {
		t.Fatal("the refresher reused the engine's handle")
	}

	if _, err := Call(eh, "refresh", `{}`); err == nil {
		t.Error("an engine handle answered \"refresh\" — the split is not enforced at the ABI, so a " +
			"host could make openrate fetch without ever constructing a refresher")
	}
	if _, err := Call(rh, "convert", `{"from":"USD","to":"ZAR"}`); err == nil {
		t.Error("a refresher handle answered \"convert\"")
	}
	if _, err := NewRefresher(rh, `{}`); err == nil {
		t.Error("a refresher was built over another refresher")
	} else if !strings.Contains(err.Error(), "refresher") {
		t.Errorf("the wrong-kind error %q does not say what kind the handle was", err)
	}
}

// TestClosingAnEngineClosesItsRefreshers: a host that closes in the obvious
// order must not leave a ticker running in its address space.
func TestClosingAnEngineClosesItsRefreshers(t *testing.T) {
	before := OpenHandles()
	eh := mustNew(t, `{"quiet":true}`)
	rh, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}
	if OpenHandles() != before+2 {
		t.Fatalf("registry holds %d handles, want %d", OpenHandles(), before+2)
	}

	Close(eh)

	if OpenHandles() != before {
		t.Fatalf("registry holds %d handles after closing the engine, want %d — the refresher "+
			"handle leaked", OpenHandles(), before)
	}
	if _, err := Call(rh, "status", `{}`); err == nil {
		t.Error("the refresher still answers after its engine was closed")
	}
	// And closing the orphan afterwards is still safe.
	Close(rh)
}

func TestConfigAndRequestMayBeAbsent(t *testing.T) {
	// Empty and "null" both mean "nothing to say" — a C caller passes NULL and
	// the cgo layer turns it into "".
	for _, cfg := range []string{"", "null", "{}", "  "} {
		h, err := New(cfg)
		if err != nil {
			t.Fatalf("New(%q): %v", cfg, err)
		}
		if got := defaultBase(t, h); got != "ZAR" {
			t.Errorf("New(%q) default base %q, want the library default ZAR", cfg, got)
		}
		for _, req := range []string{"", "null", "{}"} {
			if _, err := Call(h, "meta", req); err != nil {
				t.Errorf("meta with request %q: %v", req, err)
			}
		}
		Close(h)
	}

	// Malformed JSON is refused, and says so.
	if _, err := New(`{"base":`); err == nil {
		t.Error("New accepted truncated JSON")
	}
	h := mustNew(t, `{"quiet":true}`)
	defer Close(h)
	if _, err := Call(h, "convert", `{"from":`); err == nil {
		t.Error("convert accepted truncated JSON")
	}
}

// TestLoadThenConvertIsTheWholeZeroNetworkPath is the flow a host in another
// language actually uses when it has its own rates: build an engine, load a
// book, ask questions.
func TestLoadThenConvertIsTheWholeZeroNetworkPath(t *testing.T) {
	h := mustNew(t, `{"base":"ZAR","quiet":true}`)
	defer Close(h)

	// Before any load, the engine knows nothing and says so rather than
	// returning a zero.
	if _, err := Call(h, "convert", `{"from":"USD","to":"ZAR","amount":1}`); err == nil {
		t.Fatal("an engine that has never been loaded answered a conversion")
	} else if !strings.Contains(err.Error(), openrate.ErrUnknownPair.Error()) {
		t.Errorf("error before load is %q; want fx.ErrUnknownPair's text so a binding can match on it", err)
	}

	loaded, err := Call(h, "load", testEdges)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var lr struct {
		BuiltAt    time.Time `json:"built_at"`
		Currencies []string  `json:"currencies"`
	}
	if err := json.Unmarshal([]byte(loaded), &lr); err != nil {
		t.Fatalf("unmarshal load response: %v", err)
	}
	if len(lr.Currencies) != 4 {
		t.Fatalf("load reported currencies %v; the four in the fixture were expected", lr.Currencies)
	}

	out, err := Call(h, "convert", `{"from":"USD","to":"ZAR","amount":100}`)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var cr struct {
		From   string  `json:"from"`
		To     string  `json:"to"`
		Amount float64 `json:"amount"`
		Result float64 `json:"result"`
		Rate   struct {
			Rate    float64  `json:"rate"`
			Hops    int      `json:"hops"`
			Sources []string `json:"sources"`
		} `json:"rate"`
	}
	if err := json.Unmarshal([]byte(out), &cr); err != nil {
		t.Fatalf("unmarshal convert response: %v", err)
	}
	// beta quoted 18.52 more recently than alpha quoted 18.5, and the freshest
	// direct quote wins. The number is asserted exactly: a conversion that came
	// back "about right" would hide a units or inversion bug.
	if cr.Rate.Rate != 18.52 || cr.Result != 1852 {
		t.Errorf("USD->ZAR came back rate=%v result=%v; want 18.52 and 1852 (beta's quote is the freshest)",
			cr.Rate.Rate, cr.Result)
	}
	if cr.Rate.Hops != 1 || len(cr.Rate.Sources) != 1 || cr.Rate.Sources[0] != "beta" {
		t.Errorf("provenance came back hops=%d sources=%v; want 1 hop from beta", cr.Rate.Hops, cr.Rate.Sources)
	}

	// And the triangulated pair, which exercises the path search.
	out, err = Call(h, "convert", `{"from":"GBP","to":"ZAR","amount":1}`)
	if err != nil {
		t.Fatalf("convert GBP->ZAR: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &cr); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if want := 1.17 * 1.09 * 18.52; cr.Result != want {
		t.Errorf("GBP->ZAR came back %v; want %v (the product of the three legs, bit for bit)", cr.Result, want)
	}
	if cr.Rate.Hops != 3 {
		t.Errorf("GBP->ZAR took %d hops, want 3", cr.Rate.Hops)
	}
}

// TestRatesRejectsAnUnknownBase pins the ABI's half of a contract all three
// surfaces now share. It used to be the ABI's one deliberate difference from
// the HTTP API; HTTP moved to match it in 0.1.6, and
// TestWireParityForAnUnknownBase asserts the two together.
func TestRatesRejectsAnUnknownBase(t *testing.T) {
	h := mustNew(t, `{"base":"ZAR","quiet":true}`)
	defer Close(h)

	// An engine holding nothing says "nothing yet", not "bad request".
	out, err := Call(h, "rates", `{"base":"ZZZ"}`)
	if err != nil {
		t.Fatalf("rates on an empty engine: %v", err)
	}
	if !strings.Contains(out, `"rates":{}`) {
		t.Errorf("rates on an empty engine returned %s; want an empty book", out)
	}

	if _, err := Call(h, "load", testEdges); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := Call(h, "rates", `{"base":"ZZZ"}`); err == nil {
		t.Error("rates against a base the snapshot does not know succeeded; every surface — the ABI, " +
			"Engine.Rates and GET /api/v1/rates — refuses this input")
	} else if !strings.Contains(err.Error(), openrate.ErrUnknownBase.Error()) {
		t.Errorf("error is %q; want ErrUnknownBase's text", err)
	}
}

func TestRefresherRejectsASpecThatResolvesToNothing(t *testing.T) {
	eh := mustNew(t, `{"quiet":true}`)
	defer Close(eh)

	// fxsource.Build silently drops names it does not know, so an all-typos
	// spec yields an empty set. Accepting it would leave "ready" blocking
	// forever on rates that can never arrive.
	_, err := NewRefresher(eh, `{"sources":"ecbb,coinbse"}`)
	if err == nil {
		t.Fatal("a refresher was built over a source spec that resolved to nothing")
	}
	if !strings.Contains(err.Error(), "no sources") {
		t.Errorf("error is %q; it should say the spec resolved to no sources", err)
	}
}

func TestVersionIsReported(t *testing.T) {
	if Version == "" {
		t.Fatal("Version is empty")
	}
}

func defaultBase(t *testing.T, h uint64) string {
	t.Helper()
	out, err := Call(h, "meta", `{}`)
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	var m struct {
		DefaultBase string `json:"default_base"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	return m.DefaultBase
}
