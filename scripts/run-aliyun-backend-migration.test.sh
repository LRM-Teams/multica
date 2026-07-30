#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${ROOT_DIR}/scripts/run-aliyun-backend-migration.sh"

fail() {
  echo "$*" >&2
  exit 1
}

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
mkdir -p "$tmp_dir/bin" "$tmp_dir/deploy" "$tmp_dir/bundle"
touch "$tmp_dir/deploy/.env"
touch "$tmp_dir/bundle/docker-compose.selfhost.yml"
touch "$tmp_dir/bundle/docker-compose.aliyun.yml"
touch "$tmp_dir/bundle/docker-compose.oss.yml"

cat >"$tmp_dir/bin/docker" <<'FAKEDOCKER'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
exit "${FAKE_DOCKER_EXIT:-0}"
FAKEDOCKER
chmod +x "$tmp_dir/bin/docker"

export PATH="$tmp_dir/bin:$PATH"
export FAKE_DOCKER_LOG="$tmp_dir/docker.log"
export MULTICA_BACKEND_IMAGE=ghcr.io/lrm-teams/multica-backend
export MULTICA_WEB_IMAGE=ghcr.io/lrm-teams/multica-web
export MULTICA_DB_BRIDGE_IMAGE=ghcr.io/lrm-teams/multica-db-bridge
export MULTICA_IMAGE_TAG=sha-deadbee
export MULTICA_DEV_AGENT_PROFILE_ACCESS=true

set +e
FAKE_DOCKER_EXIT=42 bash "$SCRIPT" "$tmp_dir/deploy" "$tmp_dir/bundle" >"$tmp_dir/failure.out" 2>&1
failure_status=$?
set -e

[[ "$failure_status" -eq 42 ]] ||
  fail "migration helper must preserve the compose failure status; got $failure_status"
grep -Fq -- '::error title=Database migration failed::image=ghcr.io/lrm-teams/multica-backend:sha-deadbee exit_status=42' "$tmp_dir/failure.out" ||
  fail "migration failure must be named directly in the Actions error"
grep -Fxq -- 'compose --project-name multica --project-directory '"$tmp_dir/deploy"' --env-file '"$tmp_dir/deploy"'/.env -f '"$tmp_dir/bundle"'/docker-compose.selfhost.yml -f '"$tmp_dir/bundle"'/docker-compose.aliyun.yml -f '"$tmp_dir/bundle"'/docker-compose.oss.yml run --rm --no-deps --pull always --entrypoint ./migrate backend up' "$FAKE_DOCKER_LOG" ||
  fail "migration helper did not invoke the exact one-off compose contract"
if grep -Eq 'up -d|force-recreate|(^| )rm -f( |$)' "$FAKE_DOCKER_LOG"; then
  fail "migration failure path must not mutate any runtime service"
fi

: >"$FAKE_DOCKER_LOG"
FAKE_DOCKER_EXIT=0 bash "$SCRIPT" "$tmp_dir/deploy" "$tmp_dir/bundle" >"$tmp_dir/success.out" 2>&1
grep -Fq -- 'Database migration complete: image=ghcr.io/lrm-teams/multica-backend:sha-deadbee' "$tmp_dir/success.out" ||
  fail "migration success must identify the exact image"
[[ "$(wc -l <"$FAKE_DOCKER_LOG" | tr -d ' ')" -eq 1 ]] ||
  fail "migration helper must invoke exactly one compose command"

echo "aliyun backend migration helper ok"
