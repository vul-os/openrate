#!/usr/bin/env bash
#
# check-ui-builds.sh — G5: both UI build states compile, and the -tags noui
# binary is measurably smaller BY A MEASURED NUMBER.
#
# Why this exists
# ---------------
# `-tags noui` is supposed to compile the console out of the binary entirely.
# Nothing about that is self-evident from a diff or from a green test run: the
# tagged build could keep linking the embedded page and every test would still
# pass, because every test asks the handler what it serves and the tagged
# handler serves the stub either way. A build tag that quietly stops removing
# anything is exactly the silent no-op this repo keeps finding.
#
# So this asserts three things about two real binaries:
#
#   1. Both build.
#   2. The console's sentinel string is IN the default binary and NOT in the
#      noui one. Presence is checked as well as absence, because a grep that
#      can never match reports "absent" about every binary in the world.
#   3. The size difference is at least 90% of the bytes serve/web embeds. A
#      floor derived from the payload rather than hard-coded stays true when
#      ui.html grows, and still fails if the embed is being linked anyway
#      (which would put the delta near zero).
#
# The measured delta at the time of writing was 66,256 bytes on
# 10,035,714 (default) vs 9,969,458 (noui), against a 65,585-byte payload.
#
# Requires bash + coreutils + the go toolchain. No jq, no binutils: the
# sentinel search uses `grep -a`, which reads a binary as text, rather than
# `strings`, which is not installed everywhere.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

# The one string that identifies the console. Kept identical in
# scripts/check-embed-linkage.sh and embedtest/g5_ui_*_test.go; the Go test
# fails if ui.html stops containing it, so all three cannot silently rot apart.
SENTINEL='id="board-table"'

UI_HTML="serve/web/ui.html"
NOTICES="serve/web/THIRD-PARTY-NOTICES.txt"

fail() { echo "::error::$*" >&2; exit 1; }

# --- 0. The sentinel must exist in the source. --------------------------------
# Without this the two binary greps below are a check on a string that occurs
# nowhere, which passes forever.
if ! grep -qF "$SENTINEL" "$UI_HTML"; then
  fail "$UI_HTML no longer contains the sentinel ${SENTINEL}. Every binary check in this script and in scripts/check-embed-linkage.sh would now pass by finding nothing. Choose a new sentinel in all three places."
fi

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

# --- 1. Both states build. ----------------------------------------------------
echo "building cmd/openrate (default)"
go build -o "$out/openrate-ui" ./cmd/openrate
echo "building cmd/openrate (-tags noui)"
go build -tags noui -o "$out/openrate-noui" ./cmd/openrate

size_ui="$(wc -c < "$out/openrate-ui" | tr -d ' ')"
size_noui="$(wc -c < "$out/openrate-noui" | tr -d ' ')"
delta=$((size_ui - size_noui))

payload=$(( $(wc -c < "$UI_HTML" | tr -d ' ') + $(wc -c < "$NOTICES" | tr -d ' ') ))
floor=$((payload * 90 / 100))

echo
echo "default : ${size_ui} bytes"
echo "noui    : ${size_noui} bytes"
echo "delta   : ${delta} bytes"
echo "embedded: ${payload} bytes (${UI_HTML} + ${NOTICES}), floor ${floor}"
echo

# --- 2. The console is in one binary and not the other. -----------------------
if ! LC_ALL=C grep -aqF "$SENTINEL" "$out/openrate-ui"; then
  fail "the DEFAULT binary does not contain the console's sentinel. Either the console stopped being embedded in the normal build, or this grep cannot find the sentinel in a linked binary — in which case its verdict on the noui binary below means nothing."
fi
echo "default build contains the console (sentinel found)"

if LC_ALL=C grep -aqF "$SENTINEL" "$out/openrate-noui"; then
  fail "the -tags noui binary STILL CONTAINS the embedded console. The tag is compiling out the handler but not the bytes, so nobody is getting the smaller binary the flag promises."
fi
echo "noui build does not contain the console (sentinel absent)"

# --- 3. The delta is real. ----------------------------------------------------
if [ "$delta" -lt "$floor" ]; then
  fail "the noui binary is only ${delta} bytes smaller; at least ${floor} was expected (90% of the ${payload} bytes serve/web embeds). 'Measurably smaller' has to be a measurement."
fi

echo
echo "check-ui-builds: OK — both states build, the console is present only in the default binary, and noui saves ${delta} bytes (floor ${floor})."
