#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
READER="$ROOT_DIR/scripts/compose-environment-value.sh"
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT
marker="$test_root/must-not-execute"
literal_value="\$HOME;\$(touch ${marker})&value=still-data"

actual=$(
  printf '%s\n' \
    'POSTGRES_USER=multica_dev' \
    "POSTGRES_PASSWORD=${literal_value}" \
    'POSTGRES_DB=multica_dev' |
    bash "$READER" POSTGRES_PASSWORD
)

if [[ "$actual" != "$literal_value" ]]; then
  echo "Compose environment value was not preserved as literal data."
  exit 1
fi
if [[ -e "$marker" ]]; then
  echo "Compose environment value was executed instead of treated as data."
  exit 1
fi

if printf '%s\n' 'POSTGRES_USER=first' 'POSTGRES_USER=second' |
  bash "$READER" POSTGRES_USER >/dev/null 2>&1; then
  echo "Duplicate Compose environment keys must be rejected."
  exit 1
fi

if printf '%s\n' 'POSTGRES_DB=multica' |
  bash "$READER" POSTGRES_USER >/dev/null 2>&1; then
  echo "Missing Compose environment keys must be rejected."
  exit 1
fi

actual_default=$(
  printf '%s\n' 'POSTGRES_PASSWORD=secret' |
    bash "$READER" POSTGRES_USER multica
)
if [[ "$actual_default" != multica ]]; then
  echo "Missing Compose environment key did not use its explicit Compose default."
  exit 1
fi

echo "compose environment value reader ok"
