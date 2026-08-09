#!/usr/bin/env bash
#
# check-embed-linkage.sh — G7: a host that imports only the library links none
# of the console's bytes.
#
# Why this exists
# ---------------
# This is the stronger claim behind "openrate is embeddable", and the one that
# is least obviously true. The root openrate package still imports serve for the
# deprecated Start, and serve imports serve/web, so ui.html is reachable through
# the import graph of a program that only ever calls Convert. It stays out of
# the binary because the linker drops data nothing can reach — not because of
# anything in the source that says so. That makes it a property no reviewer can
# confirm by reading, and one that a single stray reference would silently
# reverse.
#
# Two host programs, built from embedtest's module so they can use only the
# public API:
#
#   hosts/libonly  imports openrate + fx and nothing else  -> sentinel MUST be absent
#   hosts/withui   imports serve and builds its handler    -> sentinel MUST be present
#
# The second one is the control. Without it this script is a grep for a string
# that might not be findable in a Go binary at all, and "absent" would be true
# of every binary ever produced.
#
# Requires bash + coreutils + the go toolchain.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mod="$root/embedtest"

# Identical to the sentinel in scripts/check-ui-builds.sh and
# embedtest/g5_ui_*_test.go. The Go test fails if ui.html stops containing it.
SENTINEL='id="board-table"'

UI_HTML="$root/serve/web/ui.html"
NOTICES="$root/serve/web/THIRD-PARTY-NOTICES.txt"

fail() { echo "::error::$*" >&2; exit 1; }

if ! grep -qF "$SENTINEL" "$UI_HTML"; then
  fail "$UI_HTML no longer contains the sentinel ${SENTINEL}; both checks below would pass by finding nothing."
fi

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

cd "$mod"
echo "building hosts/libonly (library only)"
go build -o "$out/libonly" ./hosts/libonly
echo "building hosts/withui  (library + serve, UI requested)"
go build -o "$out/withui" ./hosts/withui

size_lib="$(wc -c < "$out/libonly" | tr -d ' ')"
size_ui="$(wc -c < "$out/withui" | tr -d ' ')"
delta=$((size_ui - size_lib))
payload=$(( $(wc -c < "$UI_HTML" | tr -d ' ') + $(wc -c < "$NOTICES" | tr -d ' ') ))
floor=$((payload * 90 / 100))

echo
echo "libonly : ${size_lib} bytes"
echo "withui  : ${size_ui} bytes"
echo "delta   : ${delta} bytes (embedded payload ${payload}, floor ${floor})"
echo

# --- The control first. -------------------------------------------------------
# If the sentinel cannot be found in a binary that certainly contains it, no
# conclusion may be drawn from failing to find it in the other one.
if ! LC_ALL=C grep -aqF "$SENTINEL" "$out/withui"; then
  fail "hosts/withui imports serve and its binary does not contain the sentinel. This grep cannot see the embedded page in a linked binary, so its verdict on hosts/libonly below is worthless."
fi
echo "control: a host that imports serve links the console (sentinel found in withui)"

# --- The guard. ---------------------------------------------------------------
if LC_ALL=C grep -aqF "$SENTINEL" "$out/libonly"; then
  fail "a host that imports ONLY the library links the console's bytes. Something in the library now references serve/web, so every embedder pays for a UI they never asked for."
fi
echo "guard  : a host that imports only the library links no UI bytes (sentinel absent in libonly)"

if [ "$delta" -lt "$floor" ]; then
  fail "withui is only ${delta} bytes bigger than libonly; at least ${floor} was expected (90% of the ${payload} bytes serve/web embeds). The two hosts are not differing by the console, so one of them is not built the way this check assumes."
fi

echo
echo "check-embed-linkage: OK — the console costs ${delta} bytes and only a host that asks for it pays them."
