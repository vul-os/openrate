#!/usr/bin/env bash
#
# bench-ffi.sh — measure in-process (C ABI) against loopback HTTP for the same
# conversion, on this machine, right now.
#
# The spec both openrate and llmux follow says a benchmark comparing the two
# belongs in the docs, and that it must be measured rather than asserted. This
# is the measurement. It builds the shared library, starts openrate's real HTTP
# server over a fixed snapshot, and runs one C program that drives both.
#
# Usage: scripts/bench-ffi.sh [iterations]

set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
iterations="${1:-20000}"

work="$(mktemp -d)"
server_pid=""
cleanup() {
  [ -n "$server_pid" ] && kill "$server_pid" 2>/dev/null
  rm -rf "$work"
}
trap cleanup EXIT

fail() { echo "::error::bench-ffi: $*" >&2; exit 1; }

goos="$(go env GOOS)"
ext="so"
[ "$goos" = "darwin" ] && ext="dylib"

cc="${CC:-cc}"
dl_flags=()
[ "$goos" = "linux" ] && dl_flags=(-ldl)

echo "bench-ffi: building the shared library"
CGO_ENABLED=1 go build -buildmode=c-shared -o "$work/libopenrate.$ext" "$root/ffi" \
  || fail "the shared library did not build"

echo "bench-ffi: building the loopback server (openrate's real HTTP API)"
go build -o "$work/loopback" "$root/ffi/bench/loopback" || fail "the loopback server did not build"

echo "bench-ffi: compiling the benchmark"
"$cc" -std=c11 -O2 -Wall -Wextra -I "$root/ffi/include" -o "$work/bench" \
  "$root/ffi/bench/bench.c" "${dl_flags[@]+"${dl_flags[@]}"}" || fail "bench.c did not compile"

echo "bench-ffi: starting the loopback server"
"$work/loopback" "$root/ffi/bench/snapshot.json" > "$work/url" 2>"$work/server.err" &
server_pid=$!

url=""
for _ in $(seq 1 100); do
  url="$(head -1 "$work/url" 2>/dev/null)"
  [ -n "$url" ] && break
  sleep 0.05
done
[ -n "$url" ] || fail "the loopback server never printed its URL:
$(cat "$work/server.err")"
echo "bench-ffi: server at $url"

"$work/bench" "$work/libopenrate.$ext" "$root/ffi/bench/snapshot.json" "$url" "$iterations" \
  || fail "the benchmark did not complete"

echo "bench-ffi: note — the HTTP side reuses one keep-alive connection with no TLS"
echo "bench-ffi: and no proxy, which is the fastest a sidecar can possibly be."
