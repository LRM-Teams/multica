#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require_config() {
  local config=$1
  local expected=$2

  if ! grep -Fq -- "$expected" <<<"$config"; then
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
deploy_test_workflow="$(<.github/workflows/deploy-test.yml)"
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
require_config "$deploy_workflow" 'name: aliyun-dev'
require_config "$deploy_workflow" 'runs-on: [self-hosted, aliyun]'
require_config "$deploy_workflow" 'RUNNER_EXPECTED_USER: dev'
require_config "$deploy_workflow" 'uses: actions/upload-artifact@v7'
require_config "$deploy_workflow" 'uses: actions/download-artifact@v8'
require_config "$deploy_workflow" 'scripts/assert-runner-workspace-ownership.sh'
require_config "$deploy_workflow" 'scripts/assert-served-app-image-provenance.sh'
require_config "$deploy_workflow" 'scripts/compose-environment-value.sh'
require_config "$deploy_workflow" 'scripts/assert-oss-compose-credentials.sh'
require_config "$deploy_workflow" 'scripts/run-aliyun-backend-migration.sh'
require_config "$deploy_workflow" 'scripts/validate-rtc-environment.sh'
require_config "$deploy_workflow" 'docker-compose.oss.yml'
require_config "$deploy_workflow" 'AWS_ACCESS_KEY_ID: ${{ secrets.AWS_ACCESS_KEY_ID }}'
require_config "$deploy_workflow" 'AWS_SECRET_ACCESS_KEY: ${{ secrets.AWS_SECRET_ACCESS_KEY }}'
require_config "$deploy_workflow" '- name: Run database migration'
require_config "$deploy_workflow" '- name: Publish Honor assets to OSS'
require_config "$deploy_workflow" '--entrypoint ./publish_honor_assets backend'
require_config "$deploy_workflow" 'https://cdn.leagent.me/honor-assets/v1/users/user-honor-level-01.webp'
require_config "$deploy_workflow" 'https://cdn.leagent.me/honor-assets/v1/users/user-honor-level-80.webp'
require_config "$deploy_workflow" 'https://cdn.leagent.me/honor-assets/v1/agents/agent-honor-level-01.webp'
require_config "$deploy_workflow" 'https://cdn.leagent.me/honor-assets/v1/agents/agent-honor-level-30.webp'
require_config "$deploy_workflow" 'https://cdn.leagent.me/honor-assets/v1/honor-center-orbit.webp'
require_config "$deploy_workflow" 'Host-local database identity and protected speech/RTC configuration preflight passed.'
require_config "$deploy_workflow" '--project-name multica'
require_config "$deploy_workflow" 'db_user="$(compose_env_value POSTGRES_USER multica)"'
require_config "$deploy_workflow" 'db_name="$(compose_env_value POSTGRES_DB multica)"'
require_config "$deploy_workflow" 'db_password="$(compose_env_value POSTGRES_PASSWORD)"'
require_config "$deploy_workflow" 'DOUBAO_SPEECH_API_KEY: ${{ secrets.DOUBAO_SPEECH_API_KEY }}'
require_config "$deploy_workflow" 'VOLCENGINE_RTC_APP_ID: ${{ secrets.VOLCENGINE_RTC_APP_ID }}'
require_config "$deploy_workflow" 'VOLCENGINE_RTC_APP_KEY: ${{ secrets.VOLCENGINE_RTC_APP_KEY }}'
require_config "$deploy_workflow" 'VOLCENGINE_RTC_ACCESS_KEY_ID: ${{ secrets.VOLCENGINE_RTC_ACCESS_KEY_ID }}'
require_config "$deploy_workflow" 'VOLCENGINE_RTC_SECRET_ACCESS_KEY: ${{ secrets.VOLCENGINE_RTC_SECRET_ACCESS_KEY }}'
require_config "$deploy_workflow" 'VOLCENGINE_RTC_ARK_ENDPOINT_ID: ${{ secrets.VOLCENGINE_RTC_ARK_ENDPOINT_ID }}'
require_config "$deploy_workflow" 'VOLCENGINE_RTC_ASR_APP_ID: ${{ vars.VOLCENGINE_RTC_ASR_APP_ID }}'
require_config "$deploy_workflow" 'VOLCENGINE_RTC_TTS_APP_ID: ${{ vars.VOLCENGINE_RTC_TTS_APP_ID }}'
require_config "$deploy_workflow" 'VOLCENGINE_RTC_SPEECH_ACCESS_TOKEN: ${{ secrets.VOLCENGINE_RTC_SPEECH_ACCESS_TOKEN }}'
require_config "$deploy_workflow" 'VOLCENGINE_RTC_CALLBACK_SIGNATURE: ${{ secrets.VOLCENGINE_RTC_CALLBACK_SIGNATURE }}'
if grep -Fq 'VOLCENGINE_RTC_ARK_MODEL_NAME:' <<<"$deploy_workflow"; then
  echo "Deploy workflow still accepts an Ark display model in place of an endpoint."
  exit 1
fi
require_config "$deploy_workflow" 'voice_api_key="$(compose_env_value DOUBAO_SPEECH_API_KEY)"'
require_config "$deploy_workflow" 'DOUBAO_SPEECH_API_KEY must be configured in the aliyun-dev Environment.'
require_config "$deploy_workflow" 'rtc_speech_access_token="$(compose_env_value VOLCENGINE_RTC_SPEECH_ACCESS_TOKEN)"'
require_config "$deploy_workflow" 'VOLCENGINE_RTC_SPEECH_ACCESS_TOKEN must be configured in the aliyun-dev Environment.'
require_config "$deploy_workflow" 'backend_voice_configured="$('
require_config "$deploy_workflow" 'backend_rtc_speech_configured="$('
require_config "$deploy_workflow" '-U "$target_user"'
require_config "$deploy_workflow" '-d "$target_db"'
require_config "$deploy_workflow" 'env -u POSTGRES_USER -u POSTGRES_DB -u POSTGRES_PASSWORD'
require_config "$deploy_workflow" 'compose_environment="$(compose config --environment)"'
require_config "$deploy_test_workflow" 'name: test'
require_config "$deploy_test_workflow" 'runs-on: [self-hosted, s89, test]'
require_config "$deploy_test_workflow" '--project-name multica-test'
require_config "$deploy_test_workflow" 'DEPLOY_DIR: /data/multica-test'
require_config "$deploy_test_workflow" 'findmnt -n -o TARGET'
compose_environment_unset_line="$(grep -nF -- 'unset compose_environment' .github/workflows/deploy.yml | cut -d: -f1)"
oss_credential_assert_line="$(
  grep -nF -- 'bash "${RUNNER_TEMP}/multica-deploy-bundle/scripts/assert-oss-compose-credentials.sh"' \
    .github/workflows/deploy.yml |
    head -n1 |
    cut -d: -f1
)"
if [[ -z "$compose_environment_unset_line" || -z "$oss_credential_assert_line" ]] ||
  ((compose_environment_unset_line <= oss_credential_assert_line)); then
  echo "Aliyun deploy must retain compose_environment until the OSS credential assertion has consumed it."
  exit 1
fi
if [[ $(grep -Fc 'env -u POSTGRES_USER -u POSTGRES_DB -u POSTGRES_PASSWORD' <<<"$deploy_workflow") -ne 5 ]]; then
  echo "Every Aliyun deploy/verify Compose wrapper must clear ambient POSTGRES_* values."
  exit 1
fi
if grep -Fq 'compose exec -T caddy caddy reload' <<<"$deploy_workflow"; then
  echo "Caddy reload cannot observe an atomically replaced single-file bind mount."
  exit 1
fi
if grep -Fq 'environment: s89' <<<"$deploy_workflow"; then
  echo "Aliyun deploy workflow must not bind the retired s89 Environment."
  exit 1
fi
if grep -Fq 'secrets.POSTGRES_PASSWORD' <<<"$deploy_workflow"; then
  echo "Aliyun database identity must remain in the host-owned .env."
  exit 1
fi

deploy_job="$(awk '/^  deploy:/{capture=1} capture{print}' .github/workflows/deploy.yml)"
prepare_job="$(awk '/^  prepare:/{capture=1; next} /^  build:/{capture=0} capture{print}' .github/workflows/deploy.yml)"
build_job="$(awk '/^  build:/{capture=1; next} /^  deploy:/{capture=0} capture{print}' .github/workflows/deploy.yml)"
# Deliberately NOT asserted: which runner prepare/build use. That is a CI
# preference, not a deployment-safety property, and pinning it here meant every
# legitimate `runs-on` change silently reddened this test for the whole team
# (2026-07-29). What this file guards is below — the deploy job must stay on the
# target host, credentials must stay in the host .env, and the deploy must
# consume an immutable artifact rather than a git checkout.
require_config "$build_job" 'buildkitd-config-inline: |'
require_config "$build_job" '[registry."docker.io"]'
require_config "$build_job" 'mirrors = ["docker.m.daocloud.io", "docker.1ms.run"]'
if grep -Fq 'uses: actions/checkout' <<<"$deploy_job"; then
  echo "Aliyun self-hosted deploy job must consume the immutable deploy artifact, not git checkout."
  exit 1
fi
for forbidden_command in git sudo chown; do
  if grep -Eq "(^|[^[:alnum:]_.-])${forbidden_command}[[:space:]]" <<<"$deploy_job"; then
    echo "Aliyun self-hosted deploy job must not execute ${forbidden_command}."
    exit 1
  fi
done
if grep -Eq '(^|[;&|[:space:]])\.[[:space:]]+.*\.env|(^|[;&|[:space:]])source[[:space:]]+.*\.env' <<<"$deploy_job"; then
  echo "Compose dotenv files are data and must never be sourced by the deploy shell."
  exit 1
fi
if [[ $(grep -Fc 'scripts/assert-served-app-image-provenance.sh' <<<"$deploy_job") -ne 2 ]]; then
  echo "Aliyun deploy must verify served app image provenance before and after health checks."
  exit 1
fi
require_config "$deploy_job" 'pre-health'
require_config "$deploy_job" 'post-health'
require_config "$deploy_job" '"ghcr.io/${owner_lc}/multica-backend:${IMAGE_TAG}"'
require_config "$deploy_job" '"ghcr.io/${owner_lc}/multica-web:${IMAGE_TAG}"'

asset_publish_step_line="$(grep -nF -- '- name: Publish Honor assets to OSS' .github/workflows/deploy.yml | cut -d: -f1)"
migration_step_line="$(grep -nF -- '- name: Run database migration' .github/workflows/deploy.yml | cut -d: -f1)"
runtime_step_line="$(grep -nF -- '- name: Pull & restart backend + frontend + Caddy' .github/workflows/deploy.yml | cut -d: -f1)"
caddy_install_line="$(grep -nF -- 'install -m 0644 "${RUNNER_TEMP}/multica-deploy-bundle/deploy/aliyun/Caddyfile"' .github/workflows/deploy.yml | cut -d: -f1)"
first_runtime_up_line="$(grep -nF -- 'compose up -d frontend' .github/workflows/deploy.yml | cut -d: -f1)"
if [[ -z "$asset_publish_step_line" || -z "$migration_step_line" || -z "$runtime_step_line" || -z "$caddy_install_line" || -z "$first_runtime_up_line" ]] ||
  ((asset_publish_step_line >= migration_step_line)) ||
  ((asset_publish_step_line >= runtime_step_line)) ||
  ((migration_step_line >= runtime_step_line)) ||
  ((migration_step_line >= caddy_install_line)) ||
  ((migration_step_line >= first_runtime_up_line)); then
  echo "Aliyun immutable assets and database migration must complete before Caddy installation and every runtime compose up."
  exit 1
fi

bash scripts/runner-workspace-ownership.test.sh
bash scripts/served-app-image-provenance.test.sh
bash scripts/compose-environment-value.test.sh
bash scripts/assert-oss-compose-credentials.test.sh
SELFHOST_CONFIG_STATIC_ONLY=true bash scripts/run-aliyun-backend-migration.test.sh
bash scripts/validate-rtc-environment.test.sh
bash scripts/check-exit-status.test.sh

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

require_config "$s89_config" 'container_name: multica-test-caddy'
require_config "$s89_config" 'image: caddy:2.11.3@sha256:ec18ee54aab3315c22e25f3b2babda73ff8007d39b13b3bd1bfffa2f0444c7d9'
require_config "$s89_config" 'FRONTEND_ORIGIN: https://82.157.184.89'
require_config "$s89_config" 'GOOGLE_CLIENT_ID: ""'
require_config "$s89_config" 'GOOGLE_CLIENT_SECRET: ""'
require_config "$s89_config" 'GOOGLE_REDIRECT_URI: ""'
require_config "$s89_config" 'MULTICA_APP_URL: https://82.157.184.89'
require_config "$s89_config" 'MULTICA_PUBLIC_URL: https://82.157.184.89'
require_config "$s89_config" 'APP_ENV: test'
require_config "$s89_config" 'source: /data/multica-test/postgres'
require_config "$s89_config" 'source: /data/multica-test/uploads'
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
require_config "$s89_caddyfile" 'default_sni {$MULTICA_TEST_HOST:82.157.184.89}'
require_config "$s89_caddyfile" 'profile shortlived'
require_config "$s89_caddyfile" 'disable_tlsalpn_challenge'
require_config "$s89_caddyfile" '@browser_navigation header Accept *text/html*'
require_config "$s89_caddyfile" 'redir @browser_navigation https://{$MULTICA_TEST_HOST:82.157.184.89}{uri} 308'
require_config "$s89_caddyfile" '/api/daemon/ws'
require_config "$s89_caddyfile" '/api/sandbox/node/ws'

aliyun_config="$(
  docker compose \
    --project-directory "$ROOT_DIR" \
    --env-file .env.example \
    -f docker-compose.selfhost.yml \
    -f docker-compose.aliyun.yml \
    -f docker-compose.oss.yml \
    config
)"
aliyun_backend_config="$(
  docker compose \
    --project-directory "$ROOT_DIR" \
    --env-file .env.example \
    -f docker-compose.selfhost.yml \
    -f docker-compose.aliyun.yml \
    -f docker-compose.oss.yml \
    config backend
)"

require_config "$aliyun_config" 'container_name: multica-caddy'
require_config "$aliyun_config" 'FRONTEND_ORIGIN: https://www.leagent.me'
require_config "$aliyun_config" 'GOOGLE_REDIRECT_URI: https://www.leagent.me/auth/callback'
require_config "$aliyun_config" 'MULTICA_APP_URL: https://www.leagent.me'
require_config "$aliyun_config" 'MULTICA_PUBLIC_URL: https://api.leagent.me'
require_config "$aliyun_config" 'CORS_ALLOWED_ORIGINS: https://www.leagent.me,https://leagent.me'
require_config "$aliyun_config" 'COOKIE_DOMAIN: leagent.me'
require_config "$aliyun_config" 'target: /etc/caddy/Caddyfile'
require_config "$aliyun_backend_config" 'host_ip: 127.0.0.1'
require_config "$aliyun_backend_config" 'AWS_ACCESS_KEY_ID'
require_config "$aliyun_backend_config" 'AWS_SECRET_ACCESS_KEY'
require_config "$aliyun_backend_config" 'AWS_REQUEST_CHECKSUM_CALCULATION: when_required'
require_config "$aliyun_backend_config" 'AWS_RESPONSE_CHECKSUM_VALIDATION: when_required'
if grep -Fq 'host_ip: 0.0.0.0' <<<"$aliyun_backend_config"; then
  echo "Aliyun backend must not publish its raw API port on all interfaces."
  exit 1
fi
require_config "$aliyun_config" 'published: "80"'
require_config "$aliyun_config" 'published: "443"'
require_config "$aliyun_config" 'published: "8090"'

# A self-hosted runner service may carry ambient POSTGRES_* values. Compose
# gives those values precedence over --env-file, so prove the deploy wrapper
# removes them and renders the same host-dotenv identity used by preflight.
aliyun_ambient_config="$(
  POSTGRES_USER=runner_user \
    POSTGRES_DB=runner_db \
    POSTGRES_PASSWORD=runner_secret \
    docker compose \
      --project-directory "$ROOT_DIR" \
      --env-file .env.example \
      -f docker-compose.selfhost.yml \
      -f docker-compose.aliyun.yml \
      -f docker-compose.oss.yml \
      config postgres backend
)"
require_config "$aliyun_ambient_config" 'POSTGRES_USER: runner_user'
require_config "$aliyun_ambient_config" 'POSTGRES_DB: runner_db'
require_config "$aliyun_ambient_config" 'POSTGRES_PASSWORD: runner_secret'
require_config "$aliyun_ambient_config" 'DATABASE_URL: postgres://runner_user:runner_secret@postgres:5432/runner_db?sslmode=disable'

aliyun_controlled_config="$(
  POSTGRES_USER=runner_user \
    POSTGRES_DB=runner_db \
    POSTGRES_PASSWORD=runner_secret \
    env -u POSTGRES_USER -u POSTGRES_DB -u POSTGRES_PASSWORD \
      docker compose \
        --project-directory "$ROOT_DIR" \
        --env-file .env.example \
        -f docker-compose.selfhost.yml \
        -f docker-compose.aliyun.yml \
        -f docker-compose.oss.yml \
        config postgres backend
)"
require_config "$aliyun_controlled_config" 'POSTGRES_USER: multica'
require_config "$aliyun_controlled_config" 'POSTGRES_DB: multica'
require_config "$aliyun_controlled_config" 'POSTGRES_PASSWORD: multica'
require_config "$aliyun_controlled_config" 'DATABASE_URL: postgres://multica:multica@postgres:5432/multica?sslmode=disable'

aliyun_caddyfile="$(<deploy/aliyun/Caddyfile)"
require_config "$aliyun_caddyfile" '{$MULTICA_APP_HOST:www.leagent.me}'
require_config "$aliyun_caddyfile" '{$MULTICA_API_HOST:api.leagent.me}'
require_config "$aliyun_caddyfile" '{$MULTICA_ROOT_HOST:leagent.me}'
require_config "$aliyun_caddyfile" '@browser_navigation header Accept *text/html*'
require_config "$aliyun_caddyfile" 'redir @browser_navigation https://{$MULTICA_APP_HOST:www.leagent.me}{uri} 308'
require_config "$aliyun_caddyfile" '/api/daemon/ws'
require_config "$aliyun_caddyfile" '/api/sandbox/node/ws'

MULTICA_ROOT_HOST=leagent.me \
MULTICA_APP_HOST=www.leagent.me \
MULTICA_API_HOST=api.leagent.me \
MULTICA_APP_URL=https://www.leagent.me \
MULTICA_API_URL=https://api.leagent.me \
MULTICA_WS_URL=wss://api.leagent.me/ws \
MULTICA_CORS_ALLOWED_ORIGINS=https://www.leagent.me,https://leagent.me \
MULTICA_COOKIE_DOMAIN=leagent.me \
MULTICA_GOOGLE_REDIRECT_URI=https://www.leagent.me/auth/callback \
  bash scripts/validate-public-origins.sh >/dev/null

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
