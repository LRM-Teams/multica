#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKER="$ROOT_DIR/scripts/assert-runner-workspace-ownership.sh"
CURRENT_USER=$(id -un)

test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT
mkdir -p "$test_root/repo/.git/refs"
touch "$test_root/repo/.git/refs/clean"

RUNNER_EXPECTED_USER="$CURRENT_USER" \
RUNNER_WORK_ROOT="$test_root" \
  bash "$CHECKER" test-pass >/dev/null

wrong_owner=root
if [[ "$CURRENT_USER" == root ]]; then
  wrong_owner=nobody
fi

failure_log="$test_root/failure.log"
if RUNNER_EXPECTED_USER="$CURRENT_USER" \
  RUNNER_WORK_ROOT="$test_root" \
  WORKSPACE_EXPECTED_OWNER="$wrong_owner" \
    bash "$CHECKER" test-offender >"$failure_log" 2>&1; then
  echo "Expected the ownership checker to reject the wrong owner."
  exit 1
fi

if ! grep -Fq 'phase=test-offender' "$failure_log" ||
  ! grep -Fq "$test_root/repo/.git/refs/clean" "$failure_log"; then
  echo "Expected exact phase and offending path in ownership failure."
  sed -n '1,80p' "$failure_log"
  exit 1
fi

echo "runner workspace ownership checker ok"
