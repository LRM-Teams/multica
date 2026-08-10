#!/usr/bin/env bash
set -euo pipefail

required=(
  MULTICA_ROOT_HOST
  MULTICA_APP_HOST
  MULTICA_API_HOST
  MULTICA_APP_URL
  MULTICA_API_URL
  MULTICA_WS_URL
  MULTICA_CORS_ALLOWED_ORIGINS
  MULTICA_COOKIE_DOMAIN
  MULTICA_GOOGLE_REDIRECT_URI
)

for name in "${required[@]}"; do
  if [[ -z "${!name:-}" ]]; then
    echo "public origin configuration is missing ${name}" >&2
    exit 1
  fi
done

[[ "$MULTICA_ROOT_HOST" != *"://"* && "$MULTICA_ROOT_HOST" != */* ]]
[[ "$MULTICA_APP_HOST" != *"://"* && "$MULTICA_APP_HOST" != */* ]]
[[ "$MULTICA_API_HOST" != *"://"* && "$MULTICA_API_HOST" != */* ]]
[[ "$MULTICA_APP_HOST" != "$MULTICA_API_HOST" ]]
[[ "$MULTICA_APP_URL" == "https://${MULTICA_APP_HOST}" ]]
[[ "$MULTICA_API_URL" == "https://${MULTICA_API_HOST}" ]]
[[ "$MULTICA_WS_URL" == "wss://${MULTICA_API_HOST}/ws" ]]
[[ "$MULTICA_GOOGLE_REDIRECT_URI" == "${MULTICA_APP_URL}/auth/callback" ]]

cors_match=false
IFS=',' read -r -a cors_origins <<<"$MULTICA_CORS_ALLOWED_ORIGINS"
for origin in "${cors_origins[@]}"; do
  if [[ "$origin" == "$MULTICA_APP_URL" ]]; then
    cors_match=true
  fi
done
if [[ "$cors_match" != true ]]; then
  echo "MULTICA_CORS_ALLOWED_ORIGINS must include MULTICA_APP_URL" >&2
  exit 1
fi

cookie_domain="${MULTICA_COOKIE_DOMAIN#.}"
if [[ "$MULTICA_APP_HOST" != "$cookie_domain" && "$MULTICA_APP_HOST" != *."$cookie_domain" ]]; then
  echo "MULTICA_APP_HOST must be within MULTICA_COOKIE_DOMAIN" >&2
  exit 1
fi
if [[ "$MULTICA_API_HOST" != "$cookie_domain" && "$MULTICA_API_HOST" != *."$cookie_domain" ]]; then
  echo "MULTICA_API_HOST must be within MULTICA_COOKIE_DOMAIN" >&2
  exit 1
fi

echo "public origin configuration ok: app=${MULTICA_APP_URL} api=${MULTICA_API_URL} ws=${MULTICA_WS_URL}"
