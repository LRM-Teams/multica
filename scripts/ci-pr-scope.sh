#!/usr/bin/env bash
# Classify a PR/push diff into CI scope outputs (GitHub Actions $GITHUB_OUTPUT).
#
# Inputs (first match wins for the file list):
#   CHANGED_FILES          newline-separated paths
#   CHANGED_FILES_FILE     path to that list
#   BASE_REF / HEAD_REF    git range (default origin/dev ... HEAD)
#
# Optional:
#   GITHUB_EVENT_BEFORE    push before SHA; all-zero forces full
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

HEAD_REF="${HEAD_REF:-HEAD}"
BASE_REF="${BASE_REF:-origin/dev}"

is_zero_sha() {
  [[ "${1:-}" =~ ^0+$ ]]
}

force_full=false
if is_zero_sha "${GITHUB_EVENT_BEFORE:-}"; then
  force_full=true
fi

if [[ -n "${CHANGED_FILES:-}" ]]; then
  changed="$(printf '%s\n' "$CHANGED_FILES")"
elif [[ -n "${CHANGED_FILES_FILE:-}" ]]; then
  changed="$(cat "$CHANGED_FILES_FILE")"
elif [[ "$force_full" == true ]]; then
  changed=""
else
  if ! git rev-parse --verify "$BASE_REF" >/dev/null 2>&1; then
    echo "Missing base ref: $BASE_REF" >&2
    exit 1
  fi
  changed="$(git diff --name-only --diff-filter=ACDMRT "${BASE_REF}...${HEAD_REF}")"
fi

in_list() {
  local needle=$1
  grep -Fxq -- "$needle" <<<"$changed"
}

any_prefix() {
  local prefix=$1
  grep -q "^${prefix}" <<<"$changed"
}

any_exact_or_prefix() {
  local path=$1
  in_list "$path" || any_prefix "${path}/"
}

FORCE_FULL_FILES=(
  .github/workflows/ci.yml
  pnpm-lock.yaml
  pnpm-workspace.yaml
  turbo.json
  server/go.mod
  server/go.sum
  scripts/ci-pr-scope.sh
  scripts/ci-expand-go-packages.sh
  scripts/ci-turbo-web.sh
)

for path in "${FORCE_FULL_FILES[@]}"; do
  if in_list "$path"; then
    force_full=true
    break
  fi
done

frontend_mode=skip
go_mode=skip
run_selfhost=false
run_web_workflows=false
run_baked_origins=false
run_reserved_slugs=false
run_migration_numbers=false
go_seed_packages=""

if [[ "$force_full" == true ]]; then
  frontend_mode=full
  go_mode=full
  run_selfhost=true
  run_web_workflows=true
  run_baked_origins=true
  run_reserved_slugs=true
  run_migration_numbers=true
else
  if any_prefix "apps/web/" || any_prefix "packages/core/" || \
     any_prefix "packages/ui/" || any_prefix "packages/views/" || \
     any_prefix "packages/tsconfig/" || in_list "apps/web/package.json" || \
     in_list "package.json"; then
    frontend_mode=affected
  fi

  seeds=()
  add_seed() {
    local pkg=$1
    local existing
    for existing in "${seeds[@]+"${seeds[@]}"}"; do
      if [[ "$existing" == "$pkg" ]]; then
        return
      fi
    done
    seeds+=("$pkg")
  }

  while IFS= read -r file; do
    [[ -z "$file" ]] && continue
    case "$file" in
      server/go.mod|server/go.sum)
        continue
        ;;
      server/migrations/*)
        add_seed "./internal/migrations"
        add_seed "./cmd/migrate"
        ;;
      server/pkg/db/*)
        add_seed "./pkg/db"
        ;;
      server/*)
        rel="${file#server/}"
        dir="$(dirname "$rel")"
        if [[ "$dir" == "." ]]; then
          add_seed "./"
        else
          add_seed "./${dir}"
        fi
        ;;
    esac
  done <<<"$changed"

  if ((${#seeds[@]} > 0)); then
    go_mode=affected
    go_seed_packages="$(printf '%s\n' "${seeds[@]}")"
  fi

  if any_exact_or_prefix "docker-compose.selfhost.yml" || \
     in_list "scripts/selfhost-config.test.sh" || \
     in_list "Dockerfile" || any_prefix "docker-compose"; then
    run_selfhost=true
  fi

  if any_prefix ".github/workflows/" || in_list "Dockerfile"; then
    run_web_workflows=true
  fi

  if in_list "scripts/assert-baked-web-public-origins.sh" || \
     in_list "scripts/assert-baked-web-public-origins.test.sh"; then
    run_baked_origins=true
  fi

  if in_list "server/internal/handler/reserved_slugs.json" || \
     in_list "packages/core/paths/reserved-slugs.ts" || \
     in_list "scripts/generate-reserved-slugs.mjs"; then
    run_reserved_slugs=true
    if [[ "$frontend_mode" == skip ]]; then
      frontend_mode=affected
    fi
  fi

  if any_prefix "server/migrations/"; then
    run_migration_numbers=true
  fi
fi

emit() {
  local key=$1
  local value=$2
  printf '%s=%s\n' "$key" "$value"
}

emit force_full "$force_full"
emit frontend_mode "$frontend_mode"
emit go_mode "$go_mode"
emit run_selfhost "$run_selfhost"
emit run_web_workflows "$run_web_workflows"
emit run_baked_origins "$run_baked_origins"
emit run_reserved_slugs "$run_reserved_slugs"
emit run_migration_numbers "$run_migration_numbers"

if [[ -n "$go_seed_packages" ]]; then
  printf 'go_seed_packages<<EOF\n%s\nEOF\n' "$go_seed_packages"
else
  emit go_seed_packages ""
fi
