//go:build cgo

// Command ffi is openrate's C ABI: the shared library other languages load to
// use openrate genuinely in-process, rather than talking to a sidecar over
// loopback.
//
// It is built with `go build -buildmode=c-shared` (see scripts/build-ffi.sh)
// and is deliberately thin. Every decision — what a handle is, what a method
// does, what JSON goes over the boundary — lives in ffi/abi, which contains no
// cgo and is therefore covered by the ordinary `go test ./...` run. What is
// left here is the part that can only be written in cgo: turning a `char*` into
// a Go string, and a Go string into a `malloc`'d `char*` the caller frees.
//
// # The contract, in one paragraph
//
// Handles are uint64 keys into a Go-side registry; 0 is never valid. Every
// entry point that can fail takes a `char** err`; on failure it returns 0 or
// NULL and writes a `malloc`'d UTF-8 message there. Everything this library
// returns — results and error strings alike — is freed with openrate_free, and
// with nothing else: it comes from C.CString, so the host's allocator is not
// necessarily the one that made it.
//
// # What is NOT here
//
// There is no openrate_stream. openrate has no streaming operation to expose —
// it answers from a snapshot it already holds — so rather than ship an empty
// callback entry point for symmetry with llmux, the shared ABI's streaming half
// is simply absent here, and ffi/README.md says so.
package main

/*
#include <stdlib.h>
#include <stdint.h>
*/
import "C"

import (
	"unsafe"

	"github.com/vul-os/openrate/ffi/abi"
)

// main exists because -buildmode=c-shared requires a main package. It never
// runs: loading the library runs the Go runtime's initialisation, not this.
func main() {}

// abiVersion is allocated once, at load time, and never freed. openrate_abi_version
// returns a `const char*` that the host may hold for the lifetime of the
// process, which rules out handing back memory the host is expected to release.
var abiVersion = C.CString(abi.Version)

//export openrate_abi_version
func openrate_abi_version() *C.char { return abiVersion }

// openrate_new constructs an Engine and returns its handle, or 0 with *err set.
//
// config_json may be NULL. An Engine starts no goroutine, opens no socket,
// reads no environment variable and sends no packet — see ffi/README.md, and
// TestZeroPacketsThroughTheABI, which counts.
//
//export openrate_new
func openrate_new(configJSON *C.char, err **C.char) C.uint64_t {
	clearErr(err)
	h, e := abi.New(goString(configJSON))
	if e != nil {
		setErr(err, e.Error())
		return 0
	}
	return C.uint64_t(h)
}

// openrate_refresher_new constructs a Refresher over an existing engine handle
// and returns its own handle, or 0 with *err set.
//
// This is a second, separate lifecycle call on purpose. Fetching is the one
// thing openrate does that a host might not want, and collapsing it into
// openrate_new would take that choice away — which is the design flaw the
// library's Engine/Refresher split removed. Constructing a Refresher still
// sends nothing; calling its "refresh" or "start" method is what fetches.
//
//export openrate_refresher_new
func openrate_refresher_new(engine C.uint64_t, configJSON *C.char, err **C.char) C.uint64_t {
	clearErr(err)
	h, e := abi.NewRefresher(uint64(engine), goString(configJSON))
	if e != nil {
		setErr(err, e.Error())
		return 0
	}
	return C.uint64_t(h)
}

// openrate_call runs one unary method against a handle and returns a malloc'd
// UTF-8 JSON document, or NULL with *err set.
//
// Engine handles answer: convert, rates, meta, load.
// Refresher handles answer: status, refresh, start, stop, ready.
//
//export openrate_call
func openrate_call(h C.uint64_t, method *C.char, requestJSON *C.char, err **C.char) *C.char {
	clearErr(err)
	out, e := abi.Call(uint64(h), goString(method), goString(requestJSON))
	if e != nil {
		setErr(err, e.Error())
		return nil
	}
	return C.CString(out)
}

// openrate_close releases a handle. Closing an engine also stops and releases
// every refresher built over it. Closing an unknown or already-closed handle is
// a no-op: a double free from the host is a mistake, not a crash.
//
//export openrate_close
func openrate_close(h C.uint64_t) { abi.Close(uint64(h)) }

// openrate_free releases anything this library returned — call results AND
// error strings. Passing NULL is safe.
//
//export openrate_free
func openrate_free(p *C.char) {
	if p == nil {
		return
	}
	C.free(unsafe.Pointer(p))
}

// openrate_open_handles reports how many handles are currently live. It exists
// so a host's test suite can assert it closed what it opened; nothing in the
// library needs it.
//
//export openrate_open_handles
func openrate_open_handles() C.uint64_t { return C.uint64_t(abi.OpenHandles()) }

// goString accepts NULL and returns "". Every place the ABI takes a JSON
// document, "" means "nothing to say", so a host with no configuration and no
// request body passes NULL twice and gets sensible defaults.
func goString(s *C.char) string {
	if s == nil {
		return ""
	}
	return C.GoString(s)
}

// clearErr writes NULL into the out-parameter before the work starts, so a
// caller that forgot to initialise it cannot mistake a stale pointer for a
// fresh error.
func clearErr(err **C.char) {
	if err != nil {
		*err = nil
	}
}

// setErr writes a malloc'd copy of msg into the out-parameter. A caller that
// passes NULL for err has said it does not want the message; the failure is
// still reported by the return value.
func setErr(err **C.char, msg string) {
	if err == nil {
		return
	}
	*err = C.CString(msg)
}
