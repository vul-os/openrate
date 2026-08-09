#!/usr/bin/env bash
# Run the Go examples.
#
#   ./sdks/go/examples/run.sh direct     # zero packets — no network at all
#   ./sdks/go/examples/run.sh refresh    # direct, then a live fetch from ECB
#   ./sdks/go/examples/run.sh sidecar    # build the binary, spawn it, query it
#   ./sdks/go/examples/run.sh            # all three
#
# `direct` is the only one that needs nothing: no binary, no port, no internet.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

want="${1:-all}"
cd "$repo"

if [[ "$want" == "direct" || "$want" == "all" ]]; then
  echo "==> direct, engine only (no Refresher is constructed, so no socket opens)"
  go run ./sdks/go/examples/direct
fi

if [[ "$want" == "refresh" || "$want" == "all" ]]; then
  echo
  echo "==> direct, with a Refresher (THIS FETCHES FROM ECB)"
  go run ./sdks/go/examples/direct -refresh
fi

if [[ "$want" == "sidecar" || "$want" == "all" ]]; then
  echo
  echo "==> building the openrate binary"
  go build -o "$work/openrate" ./cmd/openrate
  echo "==> sidecar (child process over HTTP)"
  go run ./sdks/go/examples/sidecar -bin "$work/openrate"
fi
