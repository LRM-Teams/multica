#!/usr/bin/env bash
# LRM-694 / #1364 Step 0 — record a cache-miss import/environment baseline.
#
# Clears Vitest/Vite caches, runs the views suite with maxWorkers shaped like
# the 2-vCPU GitHub-hosted runner, and writes
# packages/views/test/baselines/import-env-baseline.json via the custom reporter.
#
# Does not claim CI wall-time recovery. Re-run on CI before asserting deltas.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(cd "$ROOT/../.." && pwd)"
OUT="${VIEWS_TEST_COST_BASELINE_OUT:-$ROOT/test/baselines/import-env-baseline.json}"
REPORTER="$ROOT/scripts/import-env-baseline-reporter.mjs"

cd "$ROOT"

rm -rf "$ROOT/node_modules/.vite" \
  "$ROOT/node_modules/.vitest" \
  "$REPO/node_modules/.vite" \
  "$REPO/node_modules/.vitest" \
  "$ROOT/node_modules/.cache/vitest" 2>/dev/null || true

export VIEWS_TEST_COST_BASELINE_OUT="$OUT"

# maxWorkers=1 matches the measured CI shape in vitest.config.ts (nproc=2,
# one worker saturates the box). isolate stays at the config default (true).
pnpm exec vitest run \
  --reporter=dot \
  --reporter="$REPORTER" \
  --maxWorkers=1 \
  "$@"

echo "Baseline written to: $OUT"
