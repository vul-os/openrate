#!/usr/bin/env bash
#
# run-examples.sh — compile and RUN both .NET examples, direct and sidecar.
#
# Builds what they need:
#   * the shared library  (scripts/build-ffi.sh)       for the direct example
#   * the openrate binary (go build ./cmd/openrate)    for the sidecar example
#
# NETWORK. The two differ, and the difference is openrate's design rather than
# an accident of the harness:
#   * the direct example is OFFLINE by default — an engine cannot fetch. Set
#     OPENRATE_ALLOW_NETWORK=1 to have it also build a refresher and fetch from
#     ECB for real.
#   * the sidecar example NEEDS a network: `openrate serve` refreshes at
#     startup, and there is no offline mode for the server. Pointing it at a
#     fake would be testing the fake.
#
# Fails closed: a missing toolchain, a library that would not build, or an
# example that exits non-zero is a FAILURE, never a skip.
#
# Usage:  sdks/dotnet/run-examples.sh [direct|sidecar]
#
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "${here}/../.." && pwd)"
which="${1:-both}"

export DOTNET_CLI_TELEMETRY_OPTOUT=1
export DOTNET_NOLOGO=1

fail() { echo "run-examples: FAIL — $*" >&2; exit 1; }

command -v go >/dev/null 2>&1 || fail "go is not on PATH"
command -v dotnet >/dev/null 2>&1 || fail "dotnet is not on PATH"
echo "run-examples: dotnet $(dotnet --version), go $(go version | awk '{print $3}')"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

# --- the shared library ------------------------------------------------------
goos="$(go env GOOS)"; goarch="$(go env GOARCH)"
case "${goos}" in
  darwin) ext="dylib" ;;
  windows) ext="dll" ;;
  *) ext="so" ;;
esac
libfile="libopenrate-${goos}-${goarch}.${ext}"
libpath="${root}/dist/ffi/${libfile}"
if [ ! -f "${libpath}" ] && [ "${which}" != "sidecar" ]; then
  echo "run-examples: building ${libfile}…"
  "${root}/scripts/build-ffi.sh" --host-only >"${tmp}/build.log" 2>&1 \
    || { cat "${tmp}/build.log" >&2; fail "the shared library did not build"; }
fi
if [ -f "${libpath}" ]; then
  echo "run-examples: library $(wc -c < "${libpath}" | tr -d ' ') bytes"
elif [ "${which}" != "sidecar" ]; then
  fail "expected a library at ${libpath}"
fi

# --- the openrate binary -----------------------------------------------------
bin="${here}/bin/openrate"
if [ ! -x "${bin}" ]; then
  echo "run-examples: building the openrate binary…"
  mkdir -p "${here}/bin"
  ( cd "${root}" && go build -o "${bin}" ./cmd/openrate )
fi

# --- build -------------------------------------------------------------------
dotnet build "${here}/examples/Examples.csproj" -v q -c Release -o "${tmp}/out" \
  >"${tmp}/dotnet.log" 2>&1 || { cat "${tmp}/dotnet.log" >&2; fail "the examples did not build"; }
echo "run-examples: built"

# --- run ---------------------------------------------------------------------
status=0
echo
OPENRATE_LIBRARY="${libpath}" OPENRATE_BINARY="${bin}" \
  dotnet "${tmp}/out/openrate-examples.dll" "${which}" || status=1

echo
[ "${status}" -eq 0 ] || fail "an example exited non-zero"
echo "run-examples: OK"
