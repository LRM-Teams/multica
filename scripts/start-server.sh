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

stop_existing_server() {
  local pids
  pids="$(lsof -ti:"$PORT" 2>/dev/null || true)"
  if [ -z "$pids" ]; then
    echo "==> No server listening on port $PORT"
    return 0
  fi

  echo "==> Stopping existing server on port $PORT (pid: ${pids//$'\n'/ })"
  # shellcheck disable=SC2086
  kill $pids 2>/dev/null || true

  for _ in 1 2 3 4 5; do
    sleep 0.2
    pids="$(lsof -ti:"$PORT" 2>/dev/null || true)"
    [ -z "$pids" ] && return 0
  done

  echo "==> Force stopping remaining process(es) on port $PORT"
  # shellcheck disable=SC2086
  kill -9 $pids 2>/dev/null || true
}

stop_existing_server

if [ ! -x server/bin/server ]; then
  echo "Missing server/bin/server. Run: make build"
  exit 1
fi

exec server/bin/server
