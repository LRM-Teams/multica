#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCOPE="$ROOT_DIR/scripts/ci-pr-scope.sh"

fail() {
  echo "$1" >&2
  exit 1
}

scope_for() {
  CHANGED_FILES="$(printf '%s\n' "$@")" "$SCOPE"
}

require_kv() {
  local output=$1
  local key=$2
  local expected=$3
  local actual
  actual="$(grep -E "^${key}=" <<<"$output" | head -n1 | cut -d= -f2-)"
  if [[ "$actual" != "$expected" ]]; then
    fail "expected ${key}=${expected}, got ${key}=${actual}"$'\n'"$output"
  fi
}

require_seed() {
  local output=$1
  local pkg=$2
  if ! grep -Fxq -- "$pkg" <<<"$output"; then
    fail "expected seed $pkg"$'\n'"$output"
  fi
}

docs_only="$(scope_for docs/readme.md)"
require_kv "$docs_only" force_full false
require_kv "$docs_only" frontend_mode skip
require_kv "$docs_only" go_mode skip
require_kv "$docs_only" run_selfhost false
require_kv "$docs_only" run_migration_numbers false

view_change="$(scope_for packages/views/research/components/foo.tsx)"
require_kv "$view_change" force_full false
require_kv "$view_change" frontend_mode affected
require_kv "$view_change" go_mode skip

handler_change="$(scope_for server/internal/handler/research_v6_create.go)"
require_kv "$handler_change" force_full false
require_kv "$handler_change" frontend_mode skip
require_kv "$handler_change" go_mode affected
require_seed "$handler_change" "./internal/handler"

migration_change="$(scope_for server/migrations/422_example.sql)"
require_kv "$migration_change" go_mode affected
require_kv "$migration_change" run_migration_numbers true
require_seed "$migration_change" "./internal/migrations"
require_seed "$migration_change" "./cmd/migrate"

lockfile="$(scope_for pnpm-lock.yaml)"
require_kv "$lockfile" force_full true
require_kv "$lockfile" frontend_mode full
require_kv "$lockfile" go_mode full
require_kv "$lockfile" run_selfhost true

ci_yml="$(scope_for .github/workflows/ci.yml)"
require_kv "$ci_yml" force_full true

scope_script="$(scope_for scripts/ci-pr-scope.sh)"
require_kv "$scope_script" force_full true

reserved="$(scope_for server/internal/handler/reserved_slugs.json)"
require_kv "$reserved" run_reserved_slugs true
require_kv "$reserved" frontend_mode affected
require_kv "$reserved" go_mode affected

workflows="$(scope_for .github/workflows/deploy-test.yml)"
require_kv "$workflows" run_web_workflows true
require_kv "$workflows" force_full false
require_kv "$workflows" frontend_mode skip

zero="$(GITHUB_EVENT_BEFORE=0000000000000000000000000000000000000000 CHANGED_FILES="" "$SCOPE")"
require_kv "$zero" force_full true
require_kv "$zero" go_mode full

echo "ci-pr-scope ok"
