#!/usr/bin/env bash
#
# go-test-gate.sh — run `go test` and FAIL if it verified nothing.
#
# Adapted from patala's scripts/go-test-gate.sh, which is this suite's working
# reference for the problem.
#
# Why this exists
# ---------------
# `go test ./...` exits 0 when every package reports `[no test files]`. The
# embeddability suite is a separate module (embedtest/) invoked by its own CI
# step, which makes it precisely the kind of thing that can stop running
# without anyone noticing: a renamed directory, a build tag that excludes every
# file, a stray -run filter, a `replace` that no longer resolves, and the step
# still prints a green tick over zero assertions.
#
# So this wrapper asserts three things after `go test` returns:
#
#   1. `go test` itself exited 0.
#   2. At least --min top-level tests actually RAN and PASSED. Zero is always a
#      failure regardless of --min.
#   3. Every --require'd test name ran and passed. A named test that was
#      skipped, filtered out or excluded by a build tag fails the gate and is
#      printed by name. That is what keeps the load-bearing ones — the
#      zero-round-trip count, the import-direction check, the internal/ wall —
#      from quietly disappearing while the suite still reports success.
#
# On failure it prints what was NOT verified. There is no skip path: an
# inconclusive run never exits 0.
#
# Usage:
#   go-test-gate.sh --min N [--require TestName]... -- <go test args...>
#
# Requires bash + coreutils + the go toolchain. No jq and no test2json: it reads
# `go test -v`'s own `--- PASS:` / `--- FAIL:` / `--- SKIP:` lines, which are
# stable, human-readable and streamed live.

set -uo pipefail

min=0
required=()

while [ $# -gt 0 ]; do
  case "$1" in
    --min)
      min="${2:?--min needs a value}"
      shift 2
      ;;
    --require)
      required+=("${2:?--require needs a test name}")
      shift 2
      ;;
    --)
      shift
      break
      ;;
    *)
      echo "go-test-gate: unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

if [ $# -eq 0 ]; then
  echo "go-test-gate: no 'go test' arguments given after --" >&2
  exit 2
fi

log="$(mktemp -t openrate-go-test.XXXXXX)"
trap 'rm -f "$log"' EXIT

# -v is required: without it there are no per-test result lines to count and
# the gate would have nothing to assert on. Streamed with tee so the run looks
# exactly like a normal `go test -v` while it happens.
set -o pipefail
go test -v "$@" 2>&1 | tee "$log"
go_rc=${PIPESTATUS[0]}

# Top-level results only (the go tool indents subtest lines), so the count is a
# stable floor that does not move when a table gains a case.
passed=$(grep -cE '^--- PASS: ' "$log")
failed=$(grep -cE '^--- FAIL: ' "$log")
skipped=$(grep -cE '^--- SKIP: ' "$log")
sub_passed=$(grep -cE '^[[:space:]]+--- PASS: ' "$log")
ran=$((passed + failed + skipped))

echo
echo "go-test-gate: go test exit=$go_rc | top-level ran=$ran passed=$passed failed=$failed skipped=$skipped | subtests passed=$sub_passed"

fail=0
note() { echo "go-test-gate: $*" >&2; fail=1; }

if [ "$go_rc" -ne 0 ]; then
  note "FAILED — go test exited $go_rc."
fi

if [ "$ran" -eq 0 ]; then
  # The precise false green this gate was written for.
  note "FAILED — ZERO tests ran. NOTHING WAS VERIFIED."
  note "  Every package reported no test files, or every test was filtered out."
  note "  Packages seen with no tests:"
  grep -E '^\?[[:space:]]' "$log" | sed 's/^/    /' >&2 || true
elif [ "$passed" -lt "$min" ]; then
  note "FAILED — only $passed top-level tests passed; at least $min are expected."
  note "  Tests were removed, renamed, or excluded (a build tag, a -run filter, a build failure)."
fi

for name in ${required+"${required[@]}"}; do
  if ! grep -qE "^--- PASS: ${name}(\$| )" "$log"; then
    note "FAILED — required test did NOT run and pass: ${name}"
    note "  NOT VERIFIED by this run: whatever ${name} asserts."
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "go-test-gate: gate FAILED — see the reasons above." >&2
  exit 1
fi

echo "go-test-gate: OK — $passed top-level tests passed (min $min), ${#required[@]} required tests present."
