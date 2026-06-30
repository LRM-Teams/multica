#!/usr/bin/env bash
# Build Go binaries and the Next.js web app for local production serving.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

SKIP_GO=false
SKIP_WEB=false

usage() {
  cat <<'EOF'
Usage: scripts/build-prod.sh [options]

Build backend (server/CLI/migrate) and frontend (@multica/web) for production mode.
Unlike `make build`, this also runs `next build` so pages are pre-compiled.

Options:
  --skip-go   Skip Go binaries (make build)
  --skip-web  Skip Next.js build
  -h, --help  Show this help

Examples:
  scripts/build-prod.sh
  scripts/build-prod.sh --skip-go    # Rebuild frontend only
  scripts/build-prod.sh --skip-web   # Rebuild backend only
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --skip-go)
      SKIP_GO=true
      ;;
    --skip-web)
      SKIP_WEB=true
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
  shift
done

if [ "$SKIP_GO" = false ]; then
  if ! command -v go >/dev/null 2>&1; then
    echo "Missing prerequisite: go" >&2
    exit 1
  fi
  echo "==> Building Go binaries -> server/bin/"
  make build
fi

if [ "$SKIP_WEB" = false ]; then
  if ! command -v pnpm >/dev/null 2>&1; then
    echo "Missing prerequisite: pnpm" >&2
    exit 1
  fi
  if [ ! -d node_modules ]; then
    echo "==> Installing dependencies..."
    pnpm install
  fi
  echo "==> Building web app (@multica/web)"
  pnpm --filter @multica/web build
fi

echo ""
echo "✓ Production build complete"
echo "  Backend:  scripts/start-server.sh"
echo "  Frontend: scripts/start-web.sh"
echo "  Daemon:   scripts/start-daemon.sh"
echo "  All-in-one: scripts/start-prod.sh"
