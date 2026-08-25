#!/usr/bin/env bash
# Expand seed Go packages to the reverse-dependency closure.
#
# stdin or GO_SEED_PACKAGES: newline-separated seeds like ./internal/handler
# stdout: space-separated packages relative to server/ (./... when GO_MODE=full)
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVER_DIR="${SERVER_DIR:-$ROOT_DIR/server}"

if [[ "${GO_MODE:-}" == full ]]; then
  printf '%s\n' "./..."
  exit 0
fi

if [[ -n "${GO_SEED_PACKAGES:-}" ]]; then
  seeds="$(printf '%s\n' "$GO_SEED_PACKAGES")"
else
  seeds="$(cat)"
fi

if [[ -z "${seeds//[[:space:]]/}" ]]; then
  exit 0
fi

cd "$SERVER_DIR"
module="$(go list -m)"

rel_from_import() {
  local import_path=$1
  if [[ "$import_path" == "$module" ]]; then
    printf './\n'
    return
  fi
  printf './%s\n' "${import_path#"$module"/}"
}

seed_imports=""
while IFS= read -r seed; do
  [[ -z "$seed" ]] && continue
  case "$seed" in
    ./...)
      printf '%s\n' "./..."
      exit 0
      ;;
    ./)
      seed_imports+="${module}"$'\n'
      ;;
    ./*)
      seed_imports+="${module}/${seed#./}"$'\n'
      ;;
    *)
      seed_imports+="${seed}"$'\n'
      ;;
  esac
done <<<"$seeds"

selected=""
while IFS=$'\t' read -r import_path deps; do
  take=false
  if grep -Fxq -- "$import_path" <<<"$seed_imports"; then
    take=true
  else
    for dep in $deps; do
      if grep -Fxq -- "$dep" <<<"$seed_imports"; then
        take=true
        break
      fi
    done
  fi
  if [[ "$take" == true ]]; then
    selected+="$(rel_from_import "$import_path")"$'\n'
  fi
done < <(go list -f '{{.ImportPath}}	{{join .Deps " "}}' ./...)

if [[ -z "${selected//[[:space:]]/}" ]]; then
  selected="$seeds"
fi

printf '%s\n' "$selected" | sed '/^$/d' | LC_ALL=C sort -u | tr '\n' ' '
printf '\n'
