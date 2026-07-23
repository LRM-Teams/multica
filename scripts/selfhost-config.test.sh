#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require_config() {
  local config=$1
  local expected=$2

  if ! grep -Fq "$expected" <<<"$config"; then
    echo "Missing expected docker compose config value:"
    echo "  $expected"
    exit 1
  fi
}

require_env() {
  local output=$1
  local expected=$2

  if ! grep -Fxq "$expected" <<<"$output"; then
    echo "Missing expected derived env value:"
    echo "  $expected"
    echo "Observed:"
    echo "$output"
    exit 1
  fi
}

compose_source="$(<docker-compose.selfhost.yml)"
require_config "$compose_source" 'db-bridge-stub-multica:'

for obsolete in \
  db-bridge-executor-multica \
  BRIDGE_MULTICA_UPSTREAM_URL \
  BRIDGE_MULTICA_UPSTREAM_API_KEY; do
  if grep -Fq "$obsolete" <<<"$compose_source"; then
    echo "Obsolete AReaL-to-MultiCA bridge setting remains: $obsolete"
    exit 1
  fi
done

deploy_workflow="$(<.github/workflows/deploy.yml)"
require_config "$deploy_workflow" 'db-bridge-stub-multica'
if grep -Fq 'db-bridge-executor-multica' <<<"$deploy_workflow"; then
  echo "Deploy workflow still manages the removed Multica-side bridge executor."
  exit 1
fi

areal_env_example="$(<db_bridge/.env.areal.example)"
require_config "$areal_env_example" 'MULTICA_BASE_URL=https://multica.example.com'
require_config "$areal_env_example" 'MULTICA_API_KEY=mul_your-multica-personal-access-token'

for obsolete in \
  AREAL_BRIDGE_STUB_URL \
  'MULTICA_BASE_URL=http://127.0.0.1:9101' \
  BRIDGE_HEADER_ENCRYPTION_KEY; do
  if grep -Fq "$obsolete" <<<"$areal_env_example"; then
    echo "Obsolete direct-call setting remains in .env.areal.example: $obsolete"
    exit 1
  fi
done

multica_env_example="$(<db_bridge/.env.multica.example)"
for obsolete in \
  'run_executor --side multica' \
  BRIDGE_MULTICA_UPSTREAM_URL \
  BRIDGE_HEADER_ENCRYPTION_KEY; do
  if grep -Fq "$obsolete" <<<"$multica_env_example"; then
    echo "Obsolete executor setting remains in .env.multica.example: $obsolete"
    exit 1
  fi
done

require_config "$deploy_workflow" 'compose up -d --no-deps --force-recreate caddy'
require_config "$deploy_workflow" 'environment: aliyun-dev'
require_config "$deploy_workflow" 'runs-on: [self-hosted, aliyun]'
require_config "$deploy_workflow" 'RUNNER_EXPECTED_USER: dev'
require_config "$deploy_workflow" 'uses: actions/upload-artifact@v4'
require_config "$deploy_workflow" 'uses: actions/download-artifact@v4'
require_config "$deploy_workflow" 'scripts/assert-runner-workspace-ownership.sh'
require_config "$deploy_workflow" 'Host-local database credential preflight passed.'
if grep -Fq 'compose exec -T caddy caddy reload' <<<"$deploy_workflow"; then
  echo "Caddy reload cannot observe an atomically replaced single-file bind mount."
  exit 1
fi
if grep -Fq 'environment: s89' <<<"$deploy_workflow"; then
  echo "Aliyun deploy workflow must not bind the retired s89 Environment."
  exit 1
fi
if grep -Fq 'secrets.POSTGRES_PASSWORD' <<<"$deploy_workflow"; then
  echo "Aliyun runtime secrets must remain in the host-owned .env."
  exit 1
fi

deploy_job="$(awk '/^  deploy:/{capture=1} capture{print}' .github/workflows/deploy.yml)"
if grep -Fq 'uses: actions/checkout' <<<"$deploy_job"; then
  echo "Aliyun self-hosted deploy job must consume the immutable deploy artifact, not git checkout."
  exit 1
fi

bash scripts/runner-workspace-ownership.test.sh

if [[ ${SELFHOST_CONFIG_STATIC_ONLY:-false} == true ]]; then
  echo "self-host static topology ok"
  exit 0
fi

config="$(
  FRONTEND_PORT=3100 BACKEND_PORT=9100 docker compose \
    --env-file .env.example \
    -f docker-compose.selfhost.yml \
    config
)"

require_config "$config" 'published: "3100"'
require_config "$config" 'published: "9100"'
require_config "$config" 'FRONTEND_ORIGIN: http://localhost:3100'
require_config "$config" 'GOOGLE_REDIRECT_URI: http://localhost:3100/auth/callback'
require_config "$config" 'MULTICA_APP_URL: http://localhost:3100'

s89_config="$(
  docker compose \
    --project-directory "$ROOT_DIR" \
    --env-file .env.example \
    -f docker-compose.selfhost.yml \
    -f docker-compose.s89.yml \
    config
)"
s89_backend_config="$(
  docker compose \
    --project-directory "$ROOT_DIR" \
    --env-file .env.example \
    -f docker-compose.selfhost.yml \
    -f docker-compose.s89.yml \
    config backend
)"

require_config "$s89_config" 'container_name: multica-caddy'
require_config "$s89_config" 'image: caddy:2.11.3@sha256:ec18ee54aab3315c22e25f3b2babda73ff8007d39b13b3bd1bfffa2f0444c7d9'
require_config "$s89_config" 'FRONTEND_ORIGIN: https://82.157.184.89'
require_config "$s89_config" 'GOOGLE_CLIENT_ID: ""'
require_config "$s89_config" 'GOOGLE_CLIENT_SECRET: ""'
require_config "$s89_config" 'GOOGLE_REDIRECT_URI: ""'
require_config "$s89_config" 'MULTICA_APP_URL: https://82.157.184.89'
require_config "$s89_config" 'MULTICA_PUBLIC_URL: https://82.157.184.89'
require_config "$s89_config" 'target: /etc/caddy/Caddyfile'
require_config "$s89_backend_config" 'host_ip: 127.0.0.1'
if grep -Fq 'host_ip: 0.0.0.0' <<<"$s89_backend_config"; then
  echo "s89 backend must not publish its raw API port on all interfaces."
  exit 1
fi
require_config "$s89_config" 'published: "80"'
require_config "$s89_config" 'published: "443"'
require_config "$s89_config" 'published: "8090"'

s89_caddyfile="$(<deploy/s89/Caddyfile)"
require_config "$s89_caddyfile" 'profile shortlived'
require_config "$s89_caddyfile" 'disable_tlsalpn_challenge'
require_config "$s89_caddyfile" '@browser_navigation header Accept *text/html*'
require_config "$s89_caddyfile" 'redir @browser_navigation https://82.157.184.89{uri} 308'
require_config "$s89_caddyfile" '/api/daemon/ws'
require_config "$s89_caddyfile" '/api/sandbox/node/ws'

aliyun_config="$(
  docker compose \
    --project-directory "$ROOT_DIR" \
    --env-file .env.example \
    -f docker-compose.selfhost.yml \
    -f docker-compose.aliyun.yml \
    config
)"
aliyun_backend_config="$(
  docker compose \
    --project-directory "$ROOT_DIR" \
    --env-file .env.example \
    -f docker-compose.selfhost.yml \
    -f docker-compose.aliyun.yml \
    config backend
)"

require_config "$aliyun_config" 'container_name: multica-caddy'
require_config "$aliyun_config" 'FRONTEND_ORIGIN: https://leagent.me'
require_config "$aliyun_config" 'GOOGLE_REDIRECT_URI: https://leagent.me/auth/callback'
require_config "$aliyun_config" 'MULTICA_APP_URL: https://leagent.me'
require_config "$aliyun_config" 'MULTICA_PUBLIC_URL: https://leagent.me'
require_config "$aliyun_config" 'target: /etc/caddy/Caddyfile'
require_config "$aliyun_backend_config" 'host_ip: 127.0.0.1'
if grep -Fq 'host_ip: 0.0.0.0' <<<"$aliyun_backend_config"; then
  echo "Aliyun backend must not publish its raw API port on all interfaces."
  exit 1
fi
require_config "$aliyun_config" 'published: "80"'
require_config "$aliyun_config" 'published: "443"'
require_config "$aliyun_config" 'published: "8090"'

aliyun_caddyfile="$(<deploy/aliyun/Caddyfile)"
require_config "$aliyun_caddyfile" 'leagent.me, www.leagent.me'
require_config "$aliyun_caddyfile" '@browser_navigation header Accept *text/html*'
require_config "$aliyun_caddyfile" 'redir @browser_navigation https://leagent.me{uri} 308'
require_config "$aliyun_caddyfile" '/api/daemon/ws'
require_config "$aliyun_caddyfile" '/api/sandbox/node/ws'

for script in scripts/dev.sh scripts/check.sh; do
  if ! grep -Fq '. scripts/local-env.sh' "$script"; then
    echo "$script must source scripts/local-env.sh for shared local env derivation."
    exit 1
  fi
done

tmp_env="$(mktemp)"
trap 'rm -f "$tmp_env"' EXIT
sed 's/^FRONTEND_PORT=.*/FRONTEND_PORT=3100/' .env.example >"$tmp_env"
printf '\nBACKEND_PORT=9100\n' >>"$tmp_env"

local_env="$(
  env -i PATH="$PATH" bash -c '
    set -euo pipefail
    env_file=$1
    set -a
    # shellcheck disable=SC1090
    . "$env_file"
    set +a
    # shellcheck disable=SC1091
    . scripts/local-env.sh
    printf "%s\n" \
      "PORT=${PORT}" \
      "FRONTEND_PORT=${FRONTEND_PORT}" \
      "FRONTEND_ORIGIN=${FRONTEND_ORIGIN}" \
      "MULTICA_APP_URL=${MULTICA_APP_URL}" \
      "GOOGLE_REDIRECT_URI=${GOOGLE_REDIRECT_URI}" \
      "MULTICA_SERVER_URL=${MULTICA_SERVER_URL}" \
      "LOCAL_UPLOAD_BASE_URL=${LOCAL_UPLOAD_BASE_URL}" \
      "PLAYWRIGHT_BASE_URL=${PLAYWRIGHT_BASE_URL}"
  ' _ "$tmp_env"
)"

require_env "$local_env" 'PORT=9100'
require_env "$local_env" 'FRONTEND_PORT=3100'
require_env "$local_env" 'FRONTEND_ORIGIN=http://localhost:3100'
require_env "$local_env" 'MULTICA_APP_URL=http://localhost:3100'
require_env "$local_env" 'GOOGLE_REDIRECT_URI=http://localhost:3100/auth/callback'
require_env "$local_env" 'MULTICA_SERVER_URL=ws://localhost:9100/ws'
require_env "$local_env" 'LOCAL_UPLOAD_BASE_URL=http://localhost:9100'
require_env "$local_env" 'PLAYWRIGHT_BASE_URL=http://localhost:3100'

echo "self-host env derivation ok"
