#!/usr/bin/env bash
# Run turbo tasks for the web product surface, either full or affected-only.
#
# Usage: ci-turbo-web.sh <full|affected> <turbo-tasks...>
# Env: BASE_REF (default origin/dev), CONCURRENCY (optional, turbo --concurrency)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

mode=${1:-}
shift || true
if [[ "$mode" != full && "$mode" != affected ]]; then
  echo "usage: ci-turbo-web.sh <full|affected> <turbo-tasks...>" >&2
  exit 2
fi
if (($# == 0)); then
  echo "usage: ci-turbo-web.sh <full|affected> <turbo-tasks...>" >&2
  exit 2
fi

base=${BASE_REF:-origin/dev}
concurrency_args=()
if [[ -n "${CONCURRENCY:-}" ]]; then
  concurrency_args=(--concurrency="$CONCURRENCY")
fi

if [[ "$mode" == full ]]; then
  exec pnpm exec turbo "$@" --filter='@multica/web...' "${concurrency_args[@]}"
fi

filters=(
  --filter="...[${base}]"
  --filter='!@multica/desktop'
  --filter='!@multica/mobile'
  --filter='!@multica/docs'
)

dry_run_file="$(mktemp)"
trap 'rm -f "$dry_run_file"' EXIT
pnpm exec turbo "$@" --dry-run=json "${filters[@]}" "${concurrency_args[@]}" >"$dry_run_file"
has_tasks="$(python3 scripts/ci-turbo-web-dry-run.py "$dry_run_file")"
if [[ "$has_tasks" == false ]]; then
  echo "No affected web packages for $* (base=$base); skipping"
  exit 0
fi
if [[ "$has_tasks" != true ]]; then
  echo "Invalid affected-package result: $has_tasks" >&2
  exit 1
fi

exec pnpm exec turbo "$@" "${filters[@]}" "${concurrency_args[@]}"
