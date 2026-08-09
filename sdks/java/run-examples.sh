#!/usr/bin/env bash
#
# run-examples.sh — compile and RUN both Java examples, direct and sidecar.
#
# Builds what they need:
#   * the shared library (scripts/build-ffi.sh)      for DirectRates
#   * the openrate binary (go build ./cmd/openrate)  for SidecarRates
#
# NETWORK. The two examples differ here, and the difference is openrate's whole
# design rather than an accident of the harness:
#   * DirectRates is OFFLINE by default. It drives an engine, which cannot
#     fetch. Set OPENRATE_ALLOW_NETWORK=1 to have it also build a refresher and
#     fetch from ECB for real.
#   * SidecarRates NEEDS a network. `openrate serve` starts its refresher on
#     startup; there is no offline mode for the server, and pointing it at a
#     fake would be testing the fake.
#
# Fails closed: a missing toolchain, a library that would not build, or an
# example that exits non-zero is a FAILURE, never a skip.
#
# Usage:  sdks/java/run-examples.sh [direct|sidecar]
#
# SidecarRates reads OPENRATE_READY_TIMEOUT_SECONDS, which shortens the wait on
# /readyz. It exists to make the failure path observable in seconds:
#
#   HTTPS_PROXY=http://127.0.0.1:1 HTTP_PROXY=http://127.0.0.1:1 \
#   OPENRATE_READY_TIMEOUT_SECONDS=8 sdks/java/run-examples.sh sidecar
#
# Every source then fails to fetch and the example exits non-zero with the
# reason /readyz reported, naming each source and its error — rather than with
# a bare timeout.
#
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "${here}/../.." && pwd)"
which="${1:-both}"

fail() { echo "run-examples: FAIL — $*" >&2; exit 1; }

for tool in go javac java; do
  command -v "${tool}" >/dev/null 2>&1 || fail "${tool} is not on PATH"
done

jdk_major="$(java -XshowSettings:properties -version 2>&1 \
  | sed -n 's/^ *java\.specification\.version *= *//p' | cut -d. -f1)"
[ -n "${jdk_major}" ] || fail "could not determine the java version"
if [ "${jdk_major}" -lt 22 ] && [ "${which}" != "sidecar" ]; then
  fail "Java ${jdk_major} is too old for the direct example — java.lang.foreign
       became permanent in Java 22. The SIDECAR example runs on Java 11+:
       sdks/java/run-examples.sh sidecar"
fi
echo "run-examples: JDK ${jdk_major} ($(java -version 2>&1 | head -1))"

tmp="$(mktemp -d)"
trap 'rm -rf "${tmp}"' EXIT

# --- the shared library ------------------------------------------------------
goos="$(go env GOOS)"; goarch="$(go env GOARCH)"
case "${goos}" in
  darwin) ext="dylib" ;;
  windows) ext="dll" ;;
  *) ext="so" ;;
esac
libpath="${root}/dist/ffi/libopenrate-${goos}-${goarch}.${ext}"
if [ ! -f "${libpath}" ] && [ "${which}" != "sidecar" ]; then
  echo "run-examples: building libopenrate-${goos}-${goarch}.${ext}…"
  "${root}/scripts/build-ffi.sh" --host-only >"${tmp}/build.log" 2>&1 \
    || { cat "${tmp}/build.log" >&2; fail "the shared library did not build"; }
fi
if [ -f "${libpath}" ]; then
  echo "run-examples: library $(wc -c < "${libpath}" | tr -d ' ') bytes at ${libpath}"
elif [ "${which}" != "sidecar" ]; then
  fail "expected a library at ${libpath}"
fi

# --- the openrate binary -----------------------------------------------------
# Rebuilt every run, not cached on existence. A binary staged before /readyz
# existed answers the readiness poll with a 404, and the example then fails
# with a message about the server rather than about the stale file that caused
# it. `go build` is incremental; a warm rebuild costs about a second.
bin="${here}/bin/openrate"
echo "run-examples: building the openrate binary…"
mkdir -p "${here}/bin"
( cd "${root}" && go build -o "${bin}" ./cmd/openrate )

# --- compile -----------------------------------------------------------------
out="${tmp}/classes"
mkdir -p "${out}"
javac -d "${out}" "${here}"/src/main/java/org/vulos/openrate/*.java
javac -d "${out}" -cp "${out}" "${here}"/examples/*.java
echo "run-examples: compiled"

status=0

if [ "${which}" = "both" ] || [ "${which}" = "direct" ]; then
  echo
  echo "================ DirectRates (in-process, C ABI) ================"
  OPENRATE_LIBRARY="${libpath}" \
    java --enable-native-access=ALL-UNNAMED -cp "${out}" DirectRates || status=1
fi

if [ "${which}" = "both" ] || [ "${which}" = "sidecar" ]; then
  echo
  echo "================ SidecarRates (child process, HTTP) ============="
  echo "(this one fetches live rates; it fails without a network)"
  OPENRATE_BINARY="${bin}" java -cp "${out}" SidecarRates || status=1
fi

echo
[ "${status}" -eq 0 ] || fail "an example exited non-zero"
echo "run-examples: OK"
