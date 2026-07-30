#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKER="$ROOT_DIR/scripts/assert-oss-compose-credentials.sh"

if ! printf '%s\n' 'POSTGRES_DB=multica' | bash "$CHECKER"; then
  echo "Local disk storage (no S3_BUCKET) must pass."
  exit 1
fi

if ! printf '%s\n' \
  'S3_BUCKET=leagent' \
  'AWS_ACCESS_KEY_ID=ak' \
  'AWS_SECRET_ACCESS_KEY=sk' |
  bash "$CHECKER"; then
  echo "OSS with both AWS keys must pass."
  exit 1
fi

if printf '%s\n' 'S3_BUCKET=leagent' | bash "$CHECKER" >/dev/null 2>&1; then
  echo "OSS without AWS keys must fail."
  exit 1
fi

if printf '%s\n' \
  'S3_BUCKET=leagent' \
  'AWS_ACCESS_KEY_ID=ak' \
  'AWS_SECRET_ACCESS_KEY=' |
  bash "$CHECKER" >/dev/null 2>&1; then
  echo "OSS with empty secret key must fail."
  exit 1
fi

echo "assert-oss-compose-credentials ok"
