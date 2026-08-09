#!/usr/bin/env bash
# Run the Rust examples.
#
#   ./sdks/rust/examples/run.sh direct     # zero packets — no network at all
#   ./sdks/rust/examples/run.sh refresh    # direct, then a live fetch from ECB
#   ./sdks/rust/examples/run.sh sidecar    # build the binary, spawn it, query it
#   ./sdks/rust/examples/run.sh            # all three
#
# `direct` needs a built libopenrate (scripts/build-ffi.sh) and nothing else:
# no binary, no port, no internet.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
crate="$(cd "$here/.." && pwd)"
repo="$(cd "$crate/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

want="${1:-all}"

lib="$repo/dist/ffi/libopenrate-darwin-arm64.dylib"
[[ -f "$lib" ]] || lib="$repo/dist/ffi/libopenrate-linux-amd64.so"

if [[ "$want" == "direct" || "$want" == "refresh" || "$want" == "all" ]]; then
  if [[ ! -f "$lib" ]]; then
    echo "==> direct SKIPPED: no libopenrate built. Run scripts/build-ffi.sh first."
  else
    if [[ "$want" == "direct" || "$want" == "all" ]]; then
      echo "==> direct, engine only (no Refresher, so no socket opens)"
      (cd "$crate" && OPENRATE_LIBRARY="$lib" cargo run --quiet --example direct)
    fi
    if [[ "$want" == "refresh" || "$want" == "all" ]]; then
      echo
      echo "==> direct, with a Refresher (THIS FETCHES FROM ECB)"
      (cd "$crate" && OPENRATE_LIBRARY="$lib" cargo run --quiet --example direct -- --refresh)
    fi
  fi
fi

if [[ "$want" == "sidecar" || "$want" == "all" ]]; then
  echo
  echo "==> building the openrate binary"
  (cd "$repo" && go build -o "$work/openrate" ./cmd/openrate)
  echo "==> sidecar (child process over HTTP)"
  (cd "$crate" && OPENRATE_BINARY="$work/openrate" cargo run --quiet --example sidecar)
fi
