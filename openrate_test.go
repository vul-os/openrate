package openrate_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/vul-os/openrate"
	"github.com/vul-os/openrate/fx"
	"github.com/vul-os/openrate/fxsource"
)

func TestStartServesAndCloses(t *testing.T) {
	// A double rather than "ecb". This test used to fetch from the European
	// Central Bank on every run of `go test`, which made the unit suite depend
	// on a third party being up and left cancelled connections winding down
	// into whatever ran next.
	openrate.StubStartSources(t, &fakeSource{name: "fake", edges: edgesAt(fixtureTime)})

	local, err := openrate.Start(openrate.Options{Sources: "fake"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer local.Close()

	resp, err := http.Get(local.BaseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}

	// The API mux is wired in-process; /meta should answer (200) even before a
	// snapshot has been fetched.
	resp, err = http.Get(local.APIBaseURL() + "/meta")
	if err != nil {
		t.Fatalf("GET /api/v1/meta: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatalf("/api/v1/meta returned 404; API not wired")
	}

	if err := local.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// Stubbing the sources above costs one thing worth buying back: nobody is left
// checking that a real source NAME still resolves. This is that check, and it
// needs no network — building a source constructs an HTTP client, it does not
// use one.
func TestTheRegistryStillResolvesARealSourceName(t *testing.T) {
	if got := fxsource.FromEnvSpec("ecb"); len(got) != 1 || got[0].Name() != "ecb" {
		t.Fatalf(`FromEnvSpec("ecb") = %v, want exactly one source named "ecb"`, got)
	}
}

func TestStartNoSources(t *testing.T) {
	if _, err := openrate.Start(openrate.Options{Sources: "nonexistent-source"}); err == nil {
		t.Fatal("expected error for empty source set, got nil")
	}
}

// blockingSource parks inside Fetch until its context is cancelled, then takes
// a moment to unwind before returning — the shape of a real source being torn
// down mid-request.
type blockingSource struct {
	entered   chan struct{}
	returned  chan struct{}
	enterOnce sync.Once
	exitOnce  sync.Once
}

func (b *blockingSource) Name() string { return "blocking" }

func (b *blockingSource) Fetch(ctx context.Context) ([]fx.Edge, error) {
	b.enterOnce.Do(func() { close(b.entered) })
	<-ctx.Done()
	// The unwind. Without it the race is too tight to observe reliably: a
	// goroutine that returns the instant its context is cancelled will usually
	// have finished before Close gets its next line out, whether or not Close
	// actually waited for it.
	time.Sleep(150 * time.Millisecond)
	b.exitOnce.Do(func() { close(b.returned) })
	return nil, ctx.Err()
}

// TestCloseWaitsForTheRefreshLoop is the other half of
// TestConstructorsSendNothing. That one asserts nothing starts until asked;
// this asserts everything has stopped once Close returns.
//
// It was not true. Close cancelled the refresher's context and then waited only
// on the SERVER's done channel, so it could return with a source Fetch still in
// flight — an outbound request outliving the engine the caller had just shut
// down. For a library that makes a point of starting nothing until asked, the
// other half has to hold too.
//
// The check is deliberately a non-blocking read taken the instant Close
// returns. No polling, no grace period: a loop that gives the fetch "a moment
// to finish" would pass against the broken version too, which is the entire
// question. Close either waited or it did not.
func TestCloseWaitsForTheRefreshLoop(t *testing.T) {
	src := &blockingSource{entered: make(chan struct{}), returned: make(chan struct{})}
	openrate.StubStartSources(t, src)

	local, err := openrate.Start(openrate.Options{Sources: "blocking"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Make sure the refresh loop is genuinely inside Fetch before shutting
	// down, or this would prove nothing on a slow machine.
	select {
	case <-src.entered:
	case <-time.After(5 * time.Second):
		_ = local.Close()
		t.Fatal("the refresher never called Fetch, so there was nothing for Close to wait for")
	}

	if err := local.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-src.returned:
	default:
		t.Fatal("Close returned while the source fetch was still running; " +
			"the refresh loop outlived the engine that owned it")
	}
}
