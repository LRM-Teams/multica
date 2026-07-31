#!/usr/bin/env bash
set -euo pipefail

# Run avatar URL backfill on the Aliyun deploy host.
# When to run: after migrate-uploads-to-s3, or whenever agent/user/workspace/
# channel.avatar_url still shows "/uploads/workspaces/..." while attachment.url
# is already on OSS (OSS-outage LocalStorage fallback window).
#
# Usage: ./scripts/run-backfill-avatar-urls.sh [/data/multica]

deploy_dir=${1:-/data/multica}
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
sql_file="${script_dir}/backfill-avatar-urls.sql"

if [[ ! -r "${deploy_dir}/.env" ]]; then
  echo "error: ${deploy_dir}/.env not readable" >&2
  exit 2
fi
if [[ ! -r "${sql_file}" ]]; then
  echo "error: ${sql_file} not found" >&2
  exit 2
fi

set -a
# shellcheck disable=SC1090
source "${deploy_dir}/.env"
set +a

: "${POSTGRES_USER:?POSTGRES_USER missing in ${deploy_dir}/.env}"
: "${POSTGRES_DB:?POSTGRES_DB missing in ${deploy_dir}/.env}"

echo "Running avatar URL backfill against database ${POSTGRES_DB}..."
docker compose \
  --project-name multica \
  --project-directory "${deploy_dir}" \
  --env-file "${deploy_dir}/.env" \
  exec -T postgres \
  psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" \
  < "${sql_file}"

echo "Avatar URL backfill complete."
