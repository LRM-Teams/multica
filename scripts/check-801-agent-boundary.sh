#!/usr/bin/env bash
# #801 acceptance runner — four Parker completion criteria in one command.
#
# Gates:
#   1. Counterfactuals ①②  (go test handler TestBoundary_Counterfactual*)
#   2. Alias call-volume 0 (delegates to measure-agent-human-route-hits.sh)
#   3. Necessary path contracts (go test cmd/multica TestBoundary_*)
#   4. Admin/human 403 surfaces (go test middleware RejectAgentOnHumanAPI*)
#
# Also runs residual source hard controls (staging, label audit, directory)
# under gate 1's package for a single handler pass.
#
# Usage:
#   DATABASE_URL=postgres://... PROM_URL=http://prometheus:9090 \
#     ./scripts/check-801-agent-boundary.sh
#   DATABASE_URL=... METRICS_URL=https://served/metrics ./scripts/check-801-agent-boundary.sh
#
# Env:
#   SERVER_DIR     — path to server module (default: <repo>/server)
#   WINDOW         — metric window for PROM (default 1h; passed through)
#   SKIP_SERVED=1  — skip gate ②; never prints DONE (unit gates only)
#
# Exit: 0=DONE (all four green), 2=NOT_DONE (one+ residual), 1=tool/setup error
# Verdict line is always last: VERDICT: DONE | NOT_DONE | UNIT_ONLY

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SERVER_DIR="${SERVER_DIR:-$ROOT/server}"
MEASURE="$ROOT/scripts/measure-agent-human-route-hits.sh"
SKIP_SERVED="${SKIP_SERVED:-0}"

if [[ ! -d "$SERVER_DIR" ]]; then
  echo "ERROR: SERVER_DIR not found: $SERVER_DIR" >&2
  exit 1
fi
if [[ ! -f "$MEASURE" ]]; then
  echo "ERROR: missing $MEASURE (Ronan criterion-② script)" >&2
  exit 1
fi
if ! command -v go >/dev/null 2>&1; then
  echo "ERROR: go required" >&2
  exit 1
fi

declare -a FAIL_REASONS=()
GATE1=0 GATE2=0 GATE3=0 GATE4=0

banner() { echo; echo "======== $* ========"; }

# --- 1: counterfactuals ①② + residual source hard controls ---
banner "1/4 Counterfactuals ①② + residual source hard controls"
if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "GATE1: FAIL — DATABASE_URL unset (handler tests need DB)"
  FAIL_REASONS+=("1 counterfactuals: DATABASE_URL unset")
  GATE1=1
else
  set +e
  (
    cd "$SERVER_DIR" &&
      go test ./internal/handler \
        -run 'TestBoundary_Counterfactual|TestBoundary_Staging|TestBoundary_LabelAttach|TestBoundary_Directory|TestAgentDirectory|TestAgentUnbound|TestBoundary_Remove|TestBoundary_Lease|TestBoundary_Derived|TestBoundary_SourceAgent' \
        -count=1 -timeout 180s
  )
  rc=$?
  set -e
  if [[ $rc -eq 0 ]]; then
    echo "GATE1: PASS — ①② + residual hard controls green"
    GATE1=0
  else
    echo "GATE1: FAIL — go test handler boundary (exit $rc)"
    FAIL_REASONS+=("1 counterfactuals/source: go test failed (exit $rc)")
    GATE1=1
  fi
fi

# --- 2: alias volume zero (Ronan script; do not reimplement) ---
banner "2/4 Alias human-route hits → ZERO"
if [[ "$SKIP_SERVED" == "1" ]]; then
  echo "GATE2: SKIPPED (SKIP_SERVED=1) — not counted as green for DONE"
  FAIL_REASONS+=("2 alias-zero: SKIPPED (set PROM_URL or METRICS_URL; unset SKIP_SERVED)")
  GATE2=1
elif [[ -z "${PROM_URL:-}" && -z "${METRICS_URL:-}" ]]; then
  echo "GATE2: FAIL — need PROM_URL or METRICS_URL (or SKIP_SERVED=1 for unit-only)"
  FAIL_REASONS+=("2 alias-zero: no PROM_URL/METRICS_URL (NO_DATA setup)")
  GATE2=1
else
  set +e
  measure_out=$("$MEASURE" 2>&1)
  measure_rc=$?
  set -e
  echo "$measure_out"
  case $measure_rc in
    0)
      echo "GATE2: PASS — ZERO residual human-route hits"
      GATE2=0
      ;;
    2)
      echo "GATE2: FAIL — RESIDUAL human-route traffic"
      FAIL_REASONS+=("2 alias-zero: RESIDUAL (see sites above / measure script)")
      GATE2=1
      ;;
    3)
      echo "GATE2: FAIL — NO_DATA (metric not deployed or wrong scrape)"
      FAIL_REASONS+=("2 alias-zero: NO_DATA (deploy metric / fix scrape)")
      GATE2=1
      ;;
    *)
      echo "GATE2: FAIL — measure script error exit=$measure_rc"
      FAIL_REASONS+=("2 alias-zero: measure script exit $measure_rc")
      GATE2=1
      ;;
  esac
fi

# --- 3: necessary path contracts ---
banner "3/4 Necessary dedicated path contracts (CLI)"
set +e
(
  cd "$SERVER_DIR" &&
    go test ./cmd/multica -run 'TestBoundary_' -count=1 -timeout 120s
)
rc=$?
set -e
if [[ $rc -eq 0 ]]; then
  echo "GATE3: PASS — necessary path contracts green"
  GATE3=0
else
  echo "GATE3: FAIL — CLI path contracts (exit $rc)"
  FAIL_REASONS+=("3 necessary paths: go test cmd/multica TestBoundary_ failed (exit $rc)")
  GATE3=1
fi

# --- 4: admin 403 surfaces ---
banner "4/4 Admin/human 403 surfaces (RejectAgentOnHumanAPI)"
set +e
(
  cd "$SERVER_DIR" &&
    go test ./internal/middleware -run 'TestRejectAgentOnHumanAPI' -count=1 -timeout 60s
)
rc=$?
set -e
if [[ $rc -eq 0 ]]; then
  echo "GATE4: PASS — 403 surface contracts green"
  GATE4=0
else
  echo "GATE4: FAIL — middleware 403 contracts (exit $rc)"
  FAIL_REASONS+=("4 admin 403: go test middleware TestRejectAgentOnHumanAPI failed (exit $rc)")
  GATE4=1
fi

# --- summary ---
banner "SUMMARY"
printf "  1 counterfactuals ①②+source : %s\n" "$([[ $GATE1 -eq 0 ]] && echo PASS || echo FAIL)"
printf "  2 alias-zero (runtime)       : %s\n" "$([[ $GATE2 -eq 0 ]] && echo PASS || echo FAIL)"
printf "  3 necessary path contracts   : %s\n" "$([[ $GATE3 -eq 0 ]] && echo PASS || echo FAIL)"
printf "  4 admin 403 surfaces         : %s\n" "$([[ $GATE4 -eq 0 ]] && echo PASS || echo FAIL)"

if [[ ${#FAIL_REASONS[@]} -gt 0 ]]; then
  echo
  echo "RESIDUAL:"
  for r in "${FAIL_REASONS[@]}"; do
    echo "  - $r"
  done
fi

if [[ $GATE1 -eq 0 && $GATE2 -eq 0 && $GATE3 -eq 0 && $GATE4 -eq 0 ]]; then
  echo
  echo "VERDICT: DONE"
  echo "#801 four completion criteria all green — safe to report done (after human final sign-off)."
  exit 0
fi

if [[ "$SKIP_SERVED" == "1" && $GATE1 -eq 0 && $GATE3 -eq 0 && $GATE4 -eq 0 ]]; then
  echo
  echo "VERDICT: UNIT_ONLY"
  echo "Unit gates 1/3/4 pass; gate 2 not evaluated. Not DONE until alias-zero ZERO on served."
  exit 2
fi

echo
echo "VERDICT: NOT_DONE"
exit 2
