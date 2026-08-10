#!/usr/bin/env bash
#
# check-ffi.sh — build the C shared library, run the C smoke test against it,
# and prove the smoke test can fail.
#
# Why this exists
# ---------------
# ffi/abi is covered by `go test ./...`, but that tests Go calling Go. The
# artifact other languages actually load is a .so/.dylib produced by
# -buildmode=c-shared, reached through dlopen and dlsym by symbol name, with C
# strings crossing the boundary and the host freeing what Go allocated. None of
# that is exercised by a Go test, and all of it can break on its own: a renamed
# export, a signature that changed shape, a header that no longer matches, a
# build tag that quietly excluded the cgo layer.
#
# So this script does what a consumer does, and then does the thing a consumer
# cannot: it deliberately breaks the library five ways and requires the smoke
# test to notice each one. A passing smoke test only means something if it is
# capable of failing, and "capable of failing" is a claim that has to be run.
#
# It also checks that the shared library carries none of the web console's
# bytes — with a control, because a grep for a string that cannot be found in a
# linked artifact would report "absent" for every binary ever produced.
#
# Usage:
#   scripts/check-ffi.sh              build, smoke test, linkage check
#   scripts/check-ffi.sh --selftest   the above, then the mutation matrix
#
# Requires bash, a C compiler, and the Go toolchain with cgo enabled.

set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
selftest=0
[ "${1:-}" = "--selftest" ] && selftest=1

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() { echo "::error::check-ffi: $*" >&2; exit 1; }

goos="$(go env GOOS)"
ext="so"
[ "$goos" = "darwin" ] && ext="dylib"
[ "$goos" = "windows" ] && ext="dll"

cc="${CC:-cc}"
command -v "$cc" >/dev/null 2>&1 || fail "no C compiler ($cc) on PATH; -buildmode=c-shared needs one"

# -ldl is required on glibc for dlopen; macOS has it in libSystem and rejects
# the flag on some toolchains, so it is added only where it is needed.
dl_flags=()
[ "$goos" = "linux" ] && dl_flags=(-ldl)

echo "check-ffi: host $goos, compiler $($cc --version 2>/dev/null | head -1)"
echo

# --- 0. the generated header line ---------------------------------------------
#
# openrate_abi_version()'s string is derived: /version.go embeds VERSION at
# compile time and ffi/abi/version.go is that value, so it cannot lag a release.
# The header's OPENRATE_ABI_VERSION macro cannot be derived the same way — the C
# preprocessor cannot read a file, and a consumer needs a literal to compare the
# runtime string against — so it is GENERATED from that same value.
#
# A generated file is only worth generating if something refuses to proceed when
# the checkout disagrees with what generation would produce. This is that
# refusal, and it is placed BEFORE the build on purpose: no shared library gets
# built out of a tree whose header lies about it. (The smoke test in step 1
# catches the same disagreement a second way, by strcmp'ing the header it was
# compiled against with the library it loaded. Both are kept: this one gives the
# one-line diagnosis and the fix, that one proves the property on the artifact.)

echo "==> the header's version macro is what \`go generate\` would write"
if ! (cd "$root" && go run ./internal/abiheader/gen -check); then
  fail "ffi/include/openrate.h is stale. Run \`go generate ./...\` and commit the result."
fi
echo

# --- 1. the library and the smoke test ---------------------------------------

lib="$work/libopenrate.$ext"
echo "==> building the shared library"
CGO_ENABLED=1 go build -C "$root/ffi" -buildmode=c-shared -o "$lib" . || fail "the shared library did not build"
echo "    $(wc -c < "$lib" | tr -d ' ') bytes"

smoke="$work/smoke"
echo "==> compiling the C smoke test (it links nothing; everything is dlsym'd)"
"$cc" -std=c11 -Wall -Wextra -Werror -I "$root/ffi/include" -o "$smoke" \
  "$root/ffi/test/smoke.c" "${dl_flags[@]+"${dl_flags[@]}"}" || fail "smoke.c did not compile"

echo "==> running the smoke test"
if ! "$smoke" "$lib"; then
  fail "the C smoke test failed against a clean build of the library"
fi
echo

# --- 2. the console is not in the shared library ------------------------------
#
# The root openrate package imports serve for the deprecated Start, and serve
# imports the embedded console, so ui.html is reachable through the import graph
# of this library. It stays out because the linker drops what nothing can reach
# — which is a property no reviewer can confirm by reading, and one stray
# reference would silently reverse. Same sentinel as
# scripts/check-embed-linkage.sh and embedtest/g5_ui_*_test.go.

SENTINEL='id="board-table"'
UI_HTML="$root/serve/web/ui.html"

echo "==> the shared library links none of the web console"
grep -qF "$SENTINEL" "$UI_HTML" || \
  fail "$UI_HTML no longer contains the sentinel ${SENTINEL}; the check below would pass by finding nothing"

# CONTROL first: a binary that certainly contains the console must be seen to
# contain it, or "absent" means nothing.
control="$work/withui"
(cd "$root/embedtest" && go build -o "$control" ./hosts/withui) || fail "could not build the control host"
if ! LC_ALL=C grep -aqF "$SENTINEL" "$control"; then
  fail "the control host imports serve and this grep cannot find the console in it, so its verdict on the shared library is worthless"
fi
echo "    control: the console is findable in a binary that links it"

if LC_ALL=C grep -aqF "$SENTINEL" "$lib"; then
  fail "the shared library contains the web console's bytes. Every language binding now ships a UI nobody asked for."
fi
echo "    guard:   the shared library contains no console bytes"
echo

if [ "$selftest" -eq 0 ]; then
  echo "check-ffi: OK — the ABI is built, loaded, and exercised."
  echo "check-ffi: run with --selftest to also prove the smoke test can fail."
  exit 0
fi

# --- 3. the mutation matrix ---------------------------------------------------
#
# Each case rebuilds the library with ONE source file replaced by a broken copy,
# using `go build -overlay` so nothing in the checkout is touched, and requires
# the SAME smoke binary to reject it. A mutation the smoke test does not notice
# is a property nobody is checking.

echo "==> selftest: breaking the library five ways, each must be caught"

mutations=0
missed=0

# mutate FILE SED_EXPR DESCRIPTION
mutate() {
  local rel="$1" expr="$2" desc="$3"
  mutations=$((mutations + 1))

  local dir="$work/mut$mutations"
  mkdir -p "$dir"
  local src="$root/$rel"
  local dst
  dst="$dir/$(basename "$rel")"

  sed "$expr" "$src" > "$dst"
  if cmp -s "$src" "$dst"; then
    fail "mutation '$desc' changed nothing in $rel — the sed expression no longer matches, so this case tests the unmodified library"
  fi

  printf '{"Replace":{"%s":"%s"}}\n' "$src" "$dst" > "$dir/overlay.json"

  local mlib="$dir/libopenrate.$ext"
  if ! CGO_ENABLED=1 go build -C "$root/ffi" -overlay "$dir/overlay.json" -buildmode=c-shared -o "$mlib" . >"$dir/build.log" 2>&1; then
    fail "mutation '$desc' did not compile; a mutation that cannot be built proves nothing:
$(cat "$dir/build.log")"
  fi

  if "$smoke" "$mlib" >"$dir/smoke.log" 2>&1; then
    missed=$((missed + 1))
    echo "    MISSED  $desc"
    sed 's/^/            /' "$dir/smoke.log" | tail -20
  else
    echo "    caught  $desc"
  fi
}

mutate "ffi/abi/engine.go" \
  's|"result": *c\.Result,|"result": c.Result * 2,|' \
  "every conversion returns twice the right answer"

mutate "ffi/abi/version.go" \
  's|^var Version = openrate\.Version$|var Version = openrate.Version + "-mutant"|' \
  "the library reports a version the header does not"

# The two cases above and below are not the same case. The one above breaks the
# ABI's copy of the version; this one breaks the LIBRARY's, one module away, and
# requires the shared library to have picked it up. That is the property the
# whole derivation rests on — that the string compiled into the .so comes from
# the VERSION file of the checkout the go.mod `replace` points at, not from
# anything anybody typed in ffi/. If this mutation is MISSED, the derivation is
# decorative and the ABI version is coming from somewhere else.
mutate "version.go" \
  's|^var Version = strings\.TrimSpace(versionFile)$|var Version = strings.TrimSpace(versionFile) + "-mutant"|' \
  "the library's own version string is not what reaches the ABI"

mutate "ffi/abi/engine.go" \
  's|return nil, fmt\.Errorf("openrate: unknown engine method.*|return marshal(map[string]any{})|' \
  "an engine answers every method name, including the refresher's"

mutate "ffi/abi/abi.go" \
  's|^\t\tdelete(objects, h)$|\t\t_ = h|' \
  "close leaves the handle in the registry, so use-after-close succeeds"

echo
if [ "$missed" -gt 0 ]; then
  fail "the smoke test did not notice $missed of $mutations deliberate defects. It passes on a clean build, but that pass is not evidence."
fi

# --- 4. the header gate can fail ----------------------------------------------
#
# Step 0 passed. That is worth nothing until it is shown to be capable of
# failing: a stale-header check that answers OK for every input is precisely
# the shape of guard this repository has shipped before.
#
# The gate is pointed at a COPY of the tree with the macro bumped, so the real
# checkout is never touched. The control runs first — an unmodified copy must
# pass through the same -root path — because if the copy were malformed the
# mutation would "fail" for the wrong reason and prove nothing.

echo "==> selftest: the header gate must reject a stale header"

hdrwork="$work/hdr"
mkdir -p "$hdrwork/ffi/include"
cp "$root/VERSION" "$hdrwork/VERSION"
cp "$root/ffi/include/openrate.h" "$hdrwork/ffi/include/openrate.h"

if ! (cd "$root" && go run ./internal/abiheader/gen -check -root "$hdrwork") >"$work/hdr-control.log" 2>&1; then
  fail "the control failed: an unmodified copy of the header does not pass the gate, so the
mutation below would fail for a reason that has nothing to do with staleness:
$(cat "$work/hdr-control.log")"
fi
echo "    control: an unmodified copy passes"

sed 's|^#define OPENRATE_ABI_VERSION .*|#define OPENRATE_ABI_VERSION "9.9.9"|' \
  "$root/ffi/include/openrate.h" > "$hdrwork/ffi/include/openrate.h"
if cmp -s "$root/ffi/include/openrate.h" "$hdrwork/ffi/include/openrate.h"; then
  fail "the header mutation changed nothing — the sed expression no longer matches the #define, so this case tests an unmodified header"
fi

if (cd "$root" && go run ./internal/abiheader/gen -check -root "$hdrwork") >"$work/hdr-mutant.log" 2>&1; then
  fail "the header gate accepted a header declaring 9.9.9. It is not checking what it claims to,
and a release could ship a header that disagrees with the library it describes."
fi
echo "    caught  a header macro that disagrees with the library"
echo

echo "check-ffi: OK — $mutations mutations plus the header gate, all caught. The checks are capable of failing."
