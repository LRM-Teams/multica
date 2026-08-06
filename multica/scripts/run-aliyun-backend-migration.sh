#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <deploy-dir> <deploy-bundle-dir>" >&2
  exit 2
fi

deploy_dir=$1
bundle_dir=$2

for variable in \
  MULTICA_BACKEND_IMAGE \
  MULTICA_WEB_IMAGE \
  MULTICA_DB_BRIDGE_IMAGE \
  MULTICA_IMAGE_TAG; do
  if [[ -z "${!variable:-}" ]]; then
    echo "::error title=Database migration configuration invalid::${variable} is empty"
    exit 2
  fi
done

if [[ ! -r "${deploy_dir}/.env" ]]; then
  echo "::error title=Database migration configuration invalid::${deploy_dir}/.env is not readable by deploy user $(id -un)"
  exit 2
fi

migration_status=0
env -u POSTGRES_USER -u POSTGRES_DB -u POSTGRES_PASSWORD \
  docker compose \
  --project-name multica \
  --project-directory "$deploy_dir" \
  --env-file "${deploy_dir}/.env" \
  -f "${bundle_dir}/docker-compose.selfhost.yml" \
  -f "${bundle_dir}/docker-compose.aliyun.yml" \
  -f "${bundle_dir}/docker-compose.oss.yml" \
  run --rm --no-deps --pull always --entrypoint ./migrate backend up ||
  migration_status=$?

if [[ "$migration_status" -ne 0 ]]; then
  echo "::error title=Database migration failed::image=${MULTICA_BACKEND_IMAGE}:${MULTICA_IMAGE_TAG} exit_status=${migration_status}"
  exit "$migration_status"
fi

echo "Database migration complete: image=${MULTICA_BACKEND_IMAGE}:${MULTICA_IMAGE_TAG}"
