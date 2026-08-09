#!/usr/bin/env bash
# Run the Swift examples.
#
#   ./sdks/swift/run.sh direct     # zero packets — no network at all
#   ./sdks/swift/run.sh refresh    # direct, then a live ECB fetch
#   ./sdks/swift/run.sh sidecar    # build the binary, spawn it, query it
#   ./sdks/swift/run.sh test
#   ./sdks/swift/run.sh            # all three examples
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

lib="$repo/dist/ffi/libopenrate-darwin-arm64.dylib"
[[ -f "$lib" ]] || lib="$repo/dist/ffi/libopenrate-linux-amd64.so"

want="${1:-all}"

if [[ "$want" == "test" ]]; then
  echo "==> swift test"
  (cd "$here" && OPENRATE_LIBRARY="$lib" swift test)
  exit 0
fi

if [[ "$want" == "direct" || "$want" == "refresh" || "$want" == "all" ]]; then
  if [[ ! -f "$lib" ]]; then
    echo "==> direct SKIPPED: no libopenrate built. Run scripts/build-ffi.sh first."
  else
    if [[ "$want" == "direct" || "$want" == "all" ]]; then
      echo "==> direct, engine only (no Refresher, so no socket opens)"
      (cd "$here" && OPENRATE_LIBRARY="$lib" swift run --quiet openrate-direct-example)
    fi
    if [[ "$want" == "refresh" || "$want" == "all" ]]; then
      echo
      echo "==> direct, with a Refresher (THIS FETCHES FROM ECB)"
      (cd "$here" && OPENRATE_LIBRARY="$lib" swift run --quiet openrate-direct-example -- --refresh)
    fi
  fi
fi

if [[ "$want" == "sidecar" || "$want" == "all" ]]; then
  echo
  echo "==> building the openrate binary"
  (cd "$repo" && go build -o "$work/openrate" ./cmd/openrate)
  echo "==> sidecar (child process over HTTP)"
  (cd "$here" && OPENRATE_BINARY="$work/openrate" swift run --quiet openrate-sidecar-example)
fi
