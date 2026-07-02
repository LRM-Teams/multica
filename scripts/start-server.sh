#!/usr/bin/env bash
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

stop_port_listeners "$PORT" "backend"

if [ ! -x server/bin/server ]; then
  echo "Missing server/bin/server. Run: make build"
  exit 1
fi

exec server/bin/server
