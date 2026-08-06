#!/usr/bin/env bash
# Start the Next.js production server (next start), not the dev server.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

ENV_FILE="${1:-.env}"

if [ ! -f "$ENV_FILE" ]; then
  echo "Missing env file: $ENV_FILE"
  exit 1
fi

set -a
# shellcheck disable=SC1090
. "$ENV_FILE"
set +a

# shellcheck disable=SC1091
. scripts/local-env.sh
# shellcheck disable=SC1091
. "$REPO_ROOT/scripts/lib/stop-port.sh"

WEB_BUILD_ID="$REPO_ROOT/apps/web/.next/BUILD_ID"
FRONTEND_HOSTNAME="${FRONTEND_HOSTNAME:-0.0.0.0}"

stop_port_listeners "$FRONTEND_PORT" "frontend"

if [ ! -f "$WEB_BUILD_ID" ]; then
  echo "Missing web production build ($WEB_BUILD_ID)."
  echo "Run: scripts/build-prod.sh"
  exit 1
fi

if ! command -v pnpm >/dev/null 2>&1; then
  echo "Missing prerequisite: pnpm" >&2
  exit 1
fi

echo "==> Starting production web server"
echo "    Local:  http://localhost:$FRONTEND_PORT"
if [ "$FRONTEND_HOSTNAME" != "127.0.0.1" ] && [ "$FRONTEND_HOSTNAME" != "localhost" ]; then
  echo "    LAN:    http://<this-host>:$FRONTEND_PORT (bind $FRONTEND_HOSTNAME)"
fi
if [ -n "${FRONTEND_ORIGIN:-}" ]; then
  echo "    Origin: $FRONTEND_ORIGIN"
fi

export NODE_ENV=production
exec pnpm --filter @multica/web exec next start \
  --port "$FRONTEND_PORT" \
  --hostname "$FRONTEND_HOSTNAME"
