#!/usr/bin/env bash
#
# run-examples.sh — compile and RUN both Kotlin examples, direct and sidecar.
#
# The Kotlin SDK wraps the Java one, so this compiles the Java sources with
# javac first and puts them on kotlinc's classpath.
#
# NETWORK, and the difference is openrate's design rather than the harness:
#   * DirectRates is OFFLINE by default — an engine cannot fetch. Set
#     OPENRATE_ALLOW_NETWORK=1 to have it also build a refresher and fetch from
#     ECB for real.
#   * SidecarRates NEEDS a network: `openrate serve` refreshes at startup.
#
# There is no coroutines dependency here: openrate has no streaming, so the
# Kotlin SDK is dependency-free apart from kotlin-stdlib. See README.md.
#
# Fails closed. Usage:  sdks/kotlin/run-examples.sh [direct|sidecar]
#
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "${here}/../.." && pwd)"
java_sdk="${root}/sdks/java"
which="${1:-both}"

fail() { echo "run-examples: FAIL — $*" >&2; exit 1; }

for tool in go javac java kotlinc; do
  command -v "${tool}" >/dev/null 2>&1 || fail "${tool} is not on PATH"
done

jdk_major="$(java -XshowSettings:properties -version 2>&1 \
  | sed -n 's/^ *java\.specification\.version *= *//p' | cut -d. -f1)"
[ -n "${jdk_major}" ] || fail "could not determine the java version"
[ "${jdk_major}" -ge 22 ] || fail "Java ${jdk_major} is too old — the Kotlin SDK compiles against
       org.vulos.openrate.OpenRateDirect, which is a Java 22 class file"
echo "run-examples: JDK ${jdk_major}, $(kotlinc -version 2>&1 | head -1)"

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
  echo "run-examples: library $(wc -c < "${libpath}" | tr -d ' ') bytes"
elif [ "${which}" != "sidecar" ]; then
  fail "expected a library at ${libpath}"
fi

# --- the openrate binary -----------------------------------------------------
bin="${java_sdk}/bin/openrate"
if [ ! -x "${bin}" ]; then
  echo "run-examples: building the openrate binary…"
  mkdir -p "${java_sdk}/bin"
  ( cd "${root}" && go build -o "${bin}" ./cmd/openrate )
fi

# --- compile -----------------------------------------------------------------
classes="${tmp}/classes"
mkdir -p "${classes}"
javac -d "${classes}" "${java_sdk}"/src/main/java/org/vulos/openrate/*.java
kotlinc -nowarn -jvm-target 22 -classpath "${classes}" -d "${classes}" \
  "${here}"/src/main/kotlin/org/vulos/openrate/kotlin/*.kt 2>&1 | grep -v '^warning:' || true
kotlinc -nowarn -jvm-target 22 -classpath "${classes}" -d "${classes}" \
  "${here}"/examples/*.kt 2>&1 | grep -v '^warning:' || true

[ -f "${classes}/DirectRatesKt.class" ] || fail "DirectRates.kt did not compile"
[ -f "${classes}/SidecarRatesKt.class" ] || fail "SidecarRates.kt did not compile"
echo "run-examples: compiled"

# kotlin-stdlib.jar has to be on the RUN classpath; kotlinc only puts it on the
# compile one. Finding it means resolving through however kotlinc was installed
# — a Homebrew symlink, an SDKMAN shim, or a plain unpacked distribution — so
# each candidate is checked and the failure names every path tried.
find_stdlib() {
  local candidates=() c bin real
  [ -n "${KOTLIN_HOME:-}" ] && candidates+=("${KOTLIN_HOME}/lib/kotlin-stdlib.jar")
  bin="$(command -v kotlinc)"
  real="${bin}"
  # Follow the symlink chain by hand: `readlink -f` is GNU and is absent on
  # some macOS versions, and a missing tool here would look like a missing jar.
  while [ -L "${real}" ]; do
    local target; target="$(readlink "${real}")"
    case "${target}" in
      /*) real="${target}" ;;
      *)  real="$(dirname "${real}")/${target}" ;;
    esac
  done
  for c in "$(dirname "$(dirname "${real}")")" "$(dirname "$(dirname "${bin}")")"; do
    candidates+=("${c}/lib/kotlin-stdlib.jar" "${c}/libexec/lib/kotlin-stdlib.jar")
  done
  if command -v brew >/dev/null 2>&1; then
    candidates+=("$(brew --prefix kotlin 2>/dev/null)/libexec/lib/kotlin-stdlib.jar")
  fi
  for c in "${candidates[@]}"; do
    if [ -f "${c}" ]; then echo "${c}"; return 0; fi
  done
  printf 'run-examples: FAIL — could not find kotlin-stdlib.jar. Tried:\n' >&2
  printf '  %s\n' "${candidates[@]}" >&2
  printf 'Set KOTLIN_HOME to the Kotlin distribution root.\n' >&2
  exit 1
}
stdlib="$(find_stdlib)"

cp_run="${classes}:${stdlib}"
status=0

if [ "${which}" = "both" ] || [ "${which}" = "direct" ]; then
  echo
  echo "================ DirectRates (in-process, C ABI) ================"
  OPENRATE_LIBRARY="${libpath}" \
    java --enable-native-access=ALL-UNNAMED -cp "${cp_run}" DirectRatesKt || status=1
fi

if [ "${which}" = "both" ] || [ "${which}" = "sidecar" ]; then
  echo
  echo "================ SidecarRates (child process, HTTP) ============="
  echo "(this one fetches live rates; it fails without a network)"
  OPENRATE_BINARY="${bin}" java -cp "${cp_run}" SidecarRatesKt || status=1
fi

echo
[ "${status}" -eq 0 ] || fail "an example exited non-zero"
echo "run-examples: OK"
