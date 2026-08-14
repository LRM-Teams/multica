#!/usr/bin/env bash
# Build (optional) and start backend + frontend in production mode.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

ENV_FILE=".env"
FORCE_BUILD=false
WITH_DAEMON=false
DAEMON_PROFILE=""

usage() {
  cat <<'EOF'
Usage: scripts/start-prod.sh [ENV_FILE] [options]

Start the compiled Go server and Next.js production server (next start).
Stops any dev server (next dev) or prior production processes on the same ports.

Options:
  --build           Run scripts/build-prod.sh before starting
  --daemon          Also start the local agent daemon
  --profile NAME    Daemon profile passed to scripts/start-daemon.sh (implies --daemon)
  -h, --help        Show this help

Examples:
  scripts/start-prod.sh
  scripts/start-prod.sh --build
  scripts/start-prod.sh .env --build --daemon
  scripts/start-prod.sh --profile local --daemon
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --build)
      FORCE_BUILD=true
      ;;
    --daemon)
      WITH_DAEMON=true
      ;;
    --profile)
      shift
      DAEMON_PROFILE="${1:?--profile requires a value}"
      WITH_DAEMON=true
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    --*)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
    *)
      ENV_FILE="$1"
      ;;
  esac
  shift
done

if [ ! -f "$ENV_FILE" ]; then
  echo "Missing env file: $ENV_FILE"
  exit 1
fi

if [ "$FORCE_BUILD" = true ]; then
  bash scripts/build-prod.sh
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

if [ ! -x server/bin/server ]; then
  echo "Missing server/bin/server. Run: scripts/build-prod.sh"
  exit 1
fi

if [ ! -f "$WEB_BUILD_ID" ]; then
  echo "Missing web production build. Run: scripts/build-prod.sh"
  exit 1
fi

if ! command -v pnpm >/dev/null 2>&1; then
  echo "Missing prerequisite: pnpm" >&2
  exit 1
fi

stop_port_listeners "$PORT" "backend"
stop_port_listeners "$FRONTEND_PORT" "frontend"

echo ""
echo "✓ Starting production stack"
echo "  Backend:  http://localhost:$PORT"
echo "  Frontend: http://localhost:$FRONTEND_PORT"
if [ -n "${FRONTEND_ORIGIN:-}" ]; then
  echo "            $FRONTEND_ORIGIN"
fi
echo ""

trap 'kill 0' EXIT

server/bin/server &
export NODE_ENV=production
pnpm --filter @multica/web exec next start \
  --port "$FRONTEND_PORT" \
  --hostname "$FRONTEND_HOSTNAME" &

if [ "$WITH_DAEMON" = true ]; then
  daemon_args=()
  if [ -n "$DAEMON_PROFILE" ]; then
    daemon_args=(--profile "$DAEMON_PROFILE")
  fi
  if ! bash scripts/start-daemon.sh "${daemon_args[@]}"; then
    echo "Warning: daemon failed to start; backend and frontend are still running." >&2
    echo "         Retry: scripts/start-daemon.sh" >&2
  fi
fi

wait
