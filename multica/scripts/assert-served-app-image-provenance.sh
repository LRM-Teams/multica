#!/usr/bin/env bash
set -euo pipefail

phase=${1:?usage: assert-served-app-image-provenance.sh <phase> <deploy-dir> <selfhost-compose> <host-compose> <oss-compose> <backend-ref> <frontend-ref>}
deploy_dir=${2:?missing deploy directory}
selfhost_compose=${3:?missing self-host Compose file}
host_compose=${4:?missing host Compose file}
oss_compose=${5:?missing OSS Compose file}
backend_ref=${6:?missing backend image reference}
frontend_ref=${7:?missing frontend image reference}

compose() {
  env -u POSTGRES_USER -u POSTGRES_DB -u POSTGRES_PASSWORD \
    docker compose \
    --project-name multica \
    --project-directory "$deploy_dir" \
    --env-file "${deploy_dir}/.env" \
    -f "$selfhost_compose" \
    -f "$host_compose" \
    -f "$oss_compose" "$@"
}

assert_service_image() {
  local service=$1
  local expected_ref=$2
  local container_id configured_ref container_image_id local_image_id

  container_id="$(compose ps -q "$service")"
  if [[ ! "$container_id" =~ ^[[:xdigit:]]{12,64}$ ]]; then
    echo "::error title=Served image provenance violation::phase=${phase} service=${service} expected_ref=${expected_ref} invalid_container_id=${container_id:-missing}"
    exit 1
  fi

  configured_ref="$(docker inspect --format '{{.Config.Image}}' "$container_id")"
  container_image_id="$(docker inspect --format '{{.Image}}' "$container_id")"
  local_image_id="$(docker image inspect --format '{{.Id}}' "$expected_ref")"

  if [ "$configured_ref" != "$expected_ref" ]; then
    echo "::error title=Served image provenance violation::phase=${phase} service=${service} container=${container_id} expected_ref=${expected_ref} configured_ref=${configured_ref:-missing}"
    exit 1
  fi
  if [[ ! "$container_image_id" =~ ^sha256:[[:xdigit:]]{64}$ ]]; then
    echo "::error title=Served image provenance violation::phase=${phase} service=${service} container=${container_id} expected_ref=${expected_ref} invalid_container_image_id=${container_image_id:-missing}"
    exit 1
  fi
  if [[ ! "$local_image_id" =~ ^sha256:[[:xdigit:]]{64}$ ]]; then
    echo "::error title=Served image provenance violation::phase=${phase} service=${service} container=${container_id} expected_ref=${expected_ref} invalid_local_image_id=${local_image_id:-missing}"
    exit 1
  fi
  if [ "$container_image_id" != "$local_image_id" ]; then
    echo "::error title=Served image provenance violation::phase=${phase} service=${service} container=${container_id} expected_ref=${expected_ref} container_image_id=${container_image_id} local_image_id=${local_image_id}"
    exit 1
  fi

  echo "Served image provenance OK: phase=${phase} service=${service} container=${container_id} configured_ref=${configured_ref} image_id=${container_image_id}"
}

assert_service_image backend "$backend_ref"
assert_service_image frontend "$frontend_ref"
