#!/usr/bin/env bash
# coverage-gate.sh — per-package Go coverage gate, extracted from knuckle's
# `just cover-check` so other org repos can adopt the same pattern without
# depending on `just` or knuckle's exact package layout.
#
# Usage:
#   coverage-gate.sh [--config FILE]
#
# Config format (one `package:threshold` per line, `#` starts a comment,
# blank lines ignored). Package paths are passed to `go test` as `<pkg>/...`.
# Thresholds are whole-percentage integers (0-100).
#
# Exit code is non-zero if any package reports no coverage or falls below its
# threshold — the same "fail CI on regression" contract knuckle uses in CI.
#
# This uses statement-count coverage from `go test -cover` (not the
# function-average Codecov reports), matching knuckle's gate.
set -euo pipefail

config=""
args=("$@")
i=0
while (( i < ${#args[@]} )); do
  arg="${args[$i]}"
  case "$arg" in
    --config)
      i=$((i + 1))
      config="${args[$i]:?--config requires a value}"
      ;;
    --config=*)
      config="${arg#*=}"
      ;;
    *)
      echo "usage: coverage-gate.sh [--config FILE]" >&2
      exit 2
      ;;
  esac
  i=$((i + 1))
done

if [[ -z "$config" ]]; then
  for candidate in coverage-gate.toml coverage-gate.conf coverage-gate.cfg; do
    if [[ -f "$candidate" ]]; then config="$candidate"; break; fi
  done
fi

if [[ -z "$config" || ! -f "$config" ]]; then
  echo "coverage-gate: no config found (looked for --config or coverage-gate.{toml,conf,cfg})" >&2
  exit 2
fi

# Parse coverage percentage out of `go test` output. `go test` prints e.g.
# "coverage: 95.5% of statements"; the value is the third field from the end.
parse_pct() {
  awk '/coverage:/ {gsub("%",""); print $(NF-2); exit}'
}

fail=0
checked=0
while IFS= read -r raw || [[ -n "$raw" ]]; do
  # strip comments and surrounding whitespace
  line="${raw%%#*}"
  line="${line#"${line%%[![:space:]]*}"}"
  line="${line%"${line##*[![:space:]]}"}"
  [[ -z "$line" ]] && continue

  # split on the last ':' (package paths contain no colon; thresholds are numeric)
  pkg="${line%:*}"
  thr="${line#*:}"
  # trim whitespace again after split
  pkg="${pkg#"${pkg%%[![:space:]]*}"}"
  pkg="${pkg%"${pkg##*[![:space:]]}"}"
  thr="${thr#"${thr%%[![:space:]]*}"}"
  thr="${thr%"${thr##*[![:space:]]}"}"

  if [[ "$pkg" != *"/"* && "$pkg" != "." ]]; then
    echo "coverage-gate: skipping malformed line (no package path): '$line'" >&2
    fail=1
    continue
  fi
  if ! [[ "$thr" =~ ^[0-9]+$ ]] || (( thr < 0 || thr > 100 )); then
    echo "coverage-gate: skipping malformed threshold (not 0-100): '$line'" >&2
    fail=1
    continue
  fi

  pct="$(go test -count=1 -cover "./${pkg}/..." 2>/dev/null | parse_pct)"
  pct="${pct%.*}"
  checked=$((checked + 1))

  if [[ -z "$pct" ]]; then
    echo "FAIL  ${pkg}   no coverage reported (target ${thr}%)"
    fail=1
  elif (( pct < thr )); then
    echo "FAIL  ${pkg}  ${pct}%  (target ${thr}%)"
    fail=1
  else
    echo "ok    ${pkg}  ${pct}%  (target ${thr}%)"
  fi
done < "$config"

echo "coverage-gate: ${checked} package(s) checked from ${config}"
exit "$fail"
