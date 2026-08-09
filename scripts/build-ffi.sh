#!/usr/bin/env bash
#
# build-ffi.sh — build libopenrate as a C shared library.
#
# `-buildmode=c-shared` needs cgo, and cgo needs a C toolchain FOR THE TARGET.
# That is the whole difficulty of shipping this: there is no `GOOS=windows go
# build` that produces a .dll without a mingw cross-compiler installed, and no
# amount of Go being "cross-compilation friendly" changes it.
#
# So this script does not pretend. It tries the host target and every cross
# target it can find a compiler for, and prints a summary that says exactly
# which artifacts exist and which were skipped and why. A target that is
# skipped is reported as skipped, never as "not needed".
#
# Usage:
#   scripts/build-ffi.sh [-o OUTDIR] [--host-only]
#
# Output (in OUTDIR, default dist/ffi/):
#   libopenrate-<goos>-<goarch>.{so,dylib,dll}   the shared library
#   libopenrate-<goos>-<goarch>.h                the header cgo generates
#   openrate.h                                   the hand-written stable header
#
# Exit status: non-zero if the HOST target failed to build. A missing cross
# toolchain is a skip, not a failure — CI builds the targets its runners have.

set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="$root/dist/ffi"
host_only=0

while [ $# -gt 0 ]; do
  case "$1" in
    -o|--out) out="${2:?-o needs a directory}"; shift 2 ;;
    --host-only) host_only=1; shift ;;
    -h|--help) sed -n '2,30p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "build-ffi: unknown argument: $1" >&2; exit 2 ;;
  esac
done

mkdir -p "$out"

host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"

built=()
skipped=()
failed=()

# ext maps a GOOS to the shared-library extension that platform's loader wants.
ext_for() {
  case "$1" in
    darwin)  echo "dylib" ;;
    windows) echo "dll" ;;
    *)       echo "so" ;;
  esac
}

# have reports whether a command exists.
have() { command -v "$1" >/dev/null 2>&1; }

# build_one GOOS GOARCH CC LABEL
#
# CC may be empty, meaning "let the toolchain pick" — correct for the host.
build_one() {
  local goos="$1" goarch="$2" cc="$3" label="$4"
  local ext name lib hdr
  ext="$(ext_for "$goos")"
  name="libopenrate-${goos}-${goarch}"
  lib="$out/${name}.${ext}"
  hdr="$out/${name}.h"

  echo "==> $label  ($goos/$goarch)"
  if [ -n "$cc" ]; then
    echo "    CC=$cc"
  fi

  # An array, not an inline `CC=...` prefix: a CC with an argument in it
  # ("clang -arch x86_64") would be word-split by the shell and run as a
  # command. That bug produced a "command not found" that looked like a missing
  # compiler rather than a broken invocation.
  local -a envv=(CGO_ENABLED=1 "GOOS=$goos" "GOARCH=$goarch")
  [ -n "$cc" ] && envv+=("CC=$cc")

  # -trimpath keeps the building machine's paths out of the artifact.
  if env "${envv[@]}" \
      go build -C "$root/ffi" -trimpath -buildmode=c-shared -o "$lib" . 2>&1 | sed 's/^/    /'; then
    # The header cgo emits lands next to the library with a matching stem.
    if [ -f "${lib%.*}.h" ] && [ "${lib%.*}.h" != "$hdr" ]; then
      mv "${lib%.*}.h" "$hdr"
    fi
    local size
    size="$(wc -c < "$lib" | tr -d ' ')"
    echo "    built ${lib#"$root"/} (${size} bytes)"
    built+=("$goos/$goarch  ${name}.${ext}  ${size} bytes")
    return 0
  fi
  echo "    FAILED"
  failed+=("$goos/$goarch")
  return 1
}

skip() {
  echo "==> $1  ($2)"
  echo "    skipped: $3"
  skipped+=("$1 — $3")
}

echo "build-ffi: openrate C shared library"
echo "build-ffi: go $(go env GOVERSION), host $host_goos/$host_goarch"
echo

# --- the host target. This one must work. -------------------------------------
host_rc=0
build_one "$host_goos" "$host_goarch" "" "host" || host_rc=1

if [ "$host_only" -eq 0 ]; then
  # --- macOS: the other architecture, via clang's -arch. ----------------------
  if [ "$host_goos" = "darwin" ]; then
    other_arch="amd64"
    [ "$host_goarch" = "amd64" ] && other_arch="arm64"
    clang_arch="x86_64"
    [ "$other_arch" = "arm64" ] && clang_arch="arm64"
    if have clang; then
      build_one darwin "$other_arch" "clang -arch $clang_arch" "macOS, other architecture" || true
    else
      skip "macOS, other architecture" "darwin/$other_arch" "no clang on PATH"
    fi
  fi

  # --- Linux. Needs a glibc cross toolchain (or zig, which bundles one). ------
  if [ "$host_goos" = "linux" ]; then
    : # the host target above already covered it
  elif have x86_64-linux-gnu-gcc; then
    build_one linux amd64 "x86_64-linux-gnu-gcc" "Linux (cross)" || true
  elif have zig; then
    build_one linux amd64 "zig cc -target x86_64-linux-gnu" "Linux (cross, via zig)" || true
  else
    skip "Linux (cross)" "linux/amd64" \
      "no x86_64-linux-gnu-gcc and no zig on PATH — build this on a Linux runner"
  fi

  # --- Windows. Needs mingw-w64. ---------------------------------------------
  if [ "$host_goos" = "windows" ]; then
    :
  elif have x86_64-w64-mingw32-gcc; then
    build_one windows amd64 "x86_64-w64-mingw32-gcc" "Windows (cross, via mingw-w64)" || true
  else
    skip "Windows (cross)" "windows/amd64" \
      "no x86_64-w64-mingw32-gcc on PATH — install mingw-w64, or build on a Windows runner"
  fi
fi

# The hand-written header is the one consumers bind against; ship it beside the
# artifacts so a downloaded release is self-contained.
cp "$root/ffi/include/openrate.h" "$out/openrate.h"

echo
echo "build-ffi: summary"
echo "  output directory: ${out#"$root"/}"
if [ ${#built[@]} -gt 0 ]; then
  echo "  BUILT:"
  for b in "${built[@]}"; do echo "    $b"; done
else
  echo "  BUILT: nothing"
fi
if [ ${#failed[@]} -gt 0 ]; then
  echo "  ATTEMPTED AND FAILED (a toolchain was found but the build did not work):"
  for f in "${failed[@]}"; do echo "    $f"; done
fi
if [ ${#skipped[@]} -gt 0 ]; then
  echo "  NOT BUILT (no toolchain on this machine):"
  for s in "${skipped[@]}"; do echo "    $s"; done
fi
echo "  header: openrate.h (hand-written, stable — bind against this one)"

if [ "$host_rc" -ne 0 ]; then
  echo
  echo "::error::build-ffi: the host target failed to build." >&2
  exit 1
fi
