package abi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vul-os/openrate/fxsource"
)

// The refresher half of the ABI, tested hermetically.
//
// Nothing here reaches the real internet — no test in this repository does. The
// adapters build their http.Client with a nil Transport, so they use
// http.DefaultTransport, and swapping that for a stub puts a whole "network"
// under the test's control while leaving every layer above it (the Refresher,
// the Engine, the ABI) exactly as a host would drive it.
//
// This file is also the far side of the zero-packet claim. It is easy to build
// an ABI that makes no requests because it makes no progress; these tests
// require the refresher path to actually fetch, so that
// TestZeroPacketsThroughTheABI's zero is a statement about the engine path
// specifically and not about a library that does nothing at all.

const ecbDailyXML = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
 <Cube>
  <Cube time="2026-08-08">
   <Cube currency="USD" rate="1.0900"/>
   <Cube currency="ZAR" rate="20.1000"/>
   <Cube currency="GBP" rate="0.8500"/>
  </Cube>
 </Cube>
</gesmes:Envelope>`

// stubTransport answers the ECB daily file and refuses everything else, so a
// test that accidentally widens its source set fails loudly instead of quietly
// reaching for a host that is not there.
type stubTransport struct {
	n atomic.Int64
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.n.Add(1)
	if req.URL.String() != fxsource.ECBDailyURL {
		return nil, &countedError{}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(ecbDailyXML)),
		Header:     http.Header{"Content-Type": []string{"application/xml"}},
		Request:    req,
	}, nil
}

// stubNetwork replaces the process's default transport for the duration of one
// test and returns the request counter.
func stubNetwork(t *testing.T) *stubTransport {
	t.Helper()
	st := &stubTransport{}
	oldTransport, oldClient := http.DefaultTransport, http.DefaultClient
	http.DefaultTransport = st
	http.DefaultClient = &http.Client{Transport: st}
	t.Cleanup(func() {
		http.DefaultTransport = oldTransport
		http.DefaultClient = oldClient
	})
	return st
}

func TestRefresherFetchesOnlyWhenAskedThroughTheABI(t *testing.T) {
	net := stubNetwork(t)

	eh := mustNew(t, `{"base":"EUR","quiet":true}`)
	defer Close(eh)
	rh, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}

	// Construction, and then a read of every non-fetching method on both
	// handles. Still nothing.
	for _, c := range []struct {
		h      uint64
		method string
	}{
		{eh, "meta"}, {eh, "rates"}, {rh, "status"},
	} {
		if _, err := Call(c.h, c.method, `{}`); err != nil {
			t.Fatalf("%s: %v", c.method, err)
		}
	}
	if got := net.n.Load(); got != 0 {
		t.Fatalf("building a refresher and reading from it made %d request(s)", got)
	}

	// Now ask for it, by name.
	out, err := Call(rh, "refresh", `{"timeout_ms":5000}`)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := net.n.Load(); got != 1 {
		t.Fatalf("refresh made %d request(s), want exactly 1", got)
	}

	var st struct {
		Sources []fxsource.Status `json:"sources"`
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("unmarshal refresh response: %v", err)
	}
	if len(st.Sources) != 1 || st.Sources[0].Name != "ecb" {
		t.Fatalf("refresh reported %+v, want one source named ecb", st.Sources)
	}
	if st.Sources[0].Edges != 3 || st.Sources[0].LastError != "" {
		t.Fatalf("ecb reported %d edges and error %q; the stub served three rates cleanly",
			st.Sources[0].Edges, st.Sources[0].LastError)
	}

	// The point of fetching: the engine can now answer.
	conv, err := Call(eh, "convert", `{"from":"EUR","to":"ZAR","amount":10}`)
	if err != nil {
		t.Fatalf("convert after refresh: %v", err)
	}
	var cr struct {
		Result float64 `json:"result"`
	}
	if err := json.Unmarshal([]byte(conv), &cr); err != nil {
		t.Fatalf("unmarshal convert: %v", err)
	}
	if cr.Result != 201 {
		t.Errorf("EUR->ZAR of 10 came back %v, want 201 from the stubbed 20.1", cr.Result)
	}

	// "ready" is satisfied now, without blocking.
	if _, err := Call(rh, "ready", `{"timeout_ms":1000}`); err != nil {
		t.Errorf("ready after a successful refresh: %v", err)
	}
}

func TestReadyTimesOutRatherThanBlockingForever(t *testing.T) {
	stubNetwork(t)
	eh := mustNew(t, `{"quiet":true}`)
	defer Close(eh)
	rh, err := NewRefresher(eh, `{"sources":"ecb","quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}

	// Nobody is refreshing, so this waits — and must give up when told to.
	// Refresher.Ready does not itself fetch, and an FFI caller with no timeout
	// mechanism of its own would otherwise hang its host thread.
	start := time.Now()
	_, err = Call(rh, "ready", `{"timeout_ms":150}`)
	if err == nil {
		t.Fatal("ready succeeded on an engine holding no rates")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("ready took %s to honour a 150ms timeout", elapsed)
	}
	if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "context") {
		t.Errorf("ready's timeout error is %q; it should name the deadline", err)
	}
}

func TestTheBackgroundLoopIsOptInAndStoppable(t *testing.T) {
	net := stubNetwork(t)

	eh := mustNew(t, `{"base":"EUR","quiet":true}`)
	defer Close(eh)
	rh, err := NewRefresher(eh, `{"sources":"ecb","interval_ms":50,"quiet":true}`)
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}

	if got := net.n.Load(); got != 0 {
		t.Fatalf("the loop fetched %d time(s) before anyone started it", got)
	}

	out, err := Call(rh, "start", `{}`)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !strings.Contains(out, `"running":true`) {
		t.Errorf("start returned %s", out)
	}

	// Starting twice is refused rather than silently doubling the tickers.
	if _, err := Call(rh, "start", `{}`); err == nil {
		t.Error("a second start was accepted; two tickers would now be feeding one engine")
	}

	if _, err := Call(rh, "ready", `{"timeout_ms":5000}`); err != nil {
		t.Fatalf("ready while the loop runs: %v", err)
	}
	if !within(3*time.Second, func() bool { return net.n.Load() >= 2 }) {
		t.Fatalf("the 50ms loop made only %d request(s) — it is not ticking", net.n.Load())
	}

	if _, err := Call(rh, "stop", `{}`); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// stop waits for the loop to exit, so the count must now be frozen.
	frozen := net.n.Load()
	time.Sleep(300 * time.Millisecond)
	if got := net.n.Load(); got != frozen {
		t.Errorf("the loop made %d more request(s) after stop returned; stop does not wait for it "+
			"to exit, so a host that closed the library could be unloading code that is still "+
			"running", got-frozen)
	}

	// And it can be started again after being stopped.
	if _, err := Call(rh, "start", `{}`); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if _, err := Call(rh, "stop", `{}`); err != nil {
		t.Fatalf("second stop: %v", err)
	}
}
