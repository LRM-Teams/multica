#!/usr/bin/env bash

set -euo pipefail

script=${1:?usage: run-aliyun-step-over-ssh.sh <script>}
: "${SSH_HOST:?SSH_HOST is required}"
: "${SSH_USER:?SSH_USER is required}"
: "${SSH_PASSWORD:?SSH_PASSWORD is required}"
: "${SSH_KNOWN_HOSTS_PATH:?SSH_KNOWN_HOSTS_PATH is required}"
: "${REMOTE_RUNNER_TEMP:?REMOTE_RUNNER_TEMP is required}"

ssh_options=(
  -o PreferredAuthentications=password
  -o PubkeyAuthentication=no
  -o StrictHostKeyChecking=yes
  -o "UserKnownHostsFile=$SSH_KNOWN_HOSTS_PATH"
)

# Only deployment inputs are forwarded. GitHub runner credentials and unrelated
# process state must not become ambient production-host environment variables.
forwarded_environment=(
  AWS_ACCESS_KEY_ID
  AWS_SECRET_ACCESS_KEY
  BRIDGE_USER_ID
  DEPLOY_DIR
  DOUBAO_DIALOG_API_KEY
  DOUBAO_SPEECH_API_KEY
  GHCR_TOKEN
  GHCR_USER
  GITHUB_REPOSITORY_OWNER
  IMAGE_TAG
  MULTICA_API_HOST
  MULTICA_API_URL
  MULTICA_APP_HOST
  MULTICA_APP_URL
  MULTICA_COOKIE_DOMAIN
  MULTICA_CORS_ALLOWED_ORIGINS
  MULTICA_GOOGLE_REDIRECT_URI
  MULTICA_ROOT_HOST
  MULTICA_WS_URL
  SUPABASE_SERVICE_ROLE_KEY
  SUPABASE_URL
  VOLCENGINE_RTC_ACCESS_KEY_ID
  VOLCENGINE_RTC_API_VERSION
  VOLCENGINE_RTC_APP_ID
  VOLCENGINE_RTC_APP_KEY
  VOLCENGINE_RTC_ARK_ENDPOINT_ID
  VOLCENGINE_RTC_ASR_APP_ID
  VOLCENGINE_RTC_CALLBACK_SIGNATURE
  VOLCENGINE_RTC_SECRET_ACCESS_KEY
  VOLCENGINE_RTC_SESSION_TOKEN
  VOLCENGINE_RTC_SPEECH_ACCESS_TOKEN
  VOLCENGINE_RTC_TTS_APP_ID
)

{
  printf 'set -euo pipefail\n'
  printf 'export HOME=%q\n' /home/dev
  printf 'export RUNNER_TEMP=%q\n' "$REMOTE_RUNNER_TEMP"
  for name in "${forwarded_environment[@]}"; do
    if [[ -v $name ]]; then
      printf 'export %s=%q\n' "$name" "${!name}"
    fi
  done
  cat "$script"
} | SSHPASS="$SSH_PASSWORD" sshpass -e \
  ssh "${ssh_options[@]}" "$SSH_USER@$SSH_HOST" \
    'exec runuser -u dev -- env HOME=/home/dev bash -se'
