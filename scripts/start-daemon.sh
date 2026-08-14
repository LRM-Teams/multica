#!/usr/bin/env bash
# Build and start the Multica daemon from this checkout's server/bin/multica.
# Stops any already-running daemon for the same profile before starting.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

MULTICA_BIN="$REPO_ROOT/server/bin/multica"
PROFILE=""
FOREGROUND=false
FORCE_BUILD=false
RESTART=true

usage() {
  cat <<'EOF'
Usage: scripts/start-daemon.sh [options]

Build server/bin/multica from the current checkout and start the local daemon.
Uses server/bin/multica (not ~/.local/bin/multica) so the running process matches
this repo.

Options:
  --build         Always run make build before starting
  --profile NAME  CLI/daemon profile (default: default profile, ~/.multica/)
  --foreground    Run in the foreground (blocks; Ctrl+C stops the daemon)
  --no-restart    Skip the automatic stop step (fails if daemon already running)
  -h, --help      Show this help

Examples:
  scripts/start-daemon.sh
  scripts/start-daemon.sh --build
  scripts/start-daemon.sh --profile local
  scripts/start-daemon.sh --foreground
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --build)
      FORCE_BUILD=true
      ;;
    --profile)
      shift
      PROFILE="${1:?--profile requires a value}"
      ;;
    --foreground)
      FOREGROUND=true
      ;;
    --no-restart)
      RESTART=false
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

profile_args=()
if [ -n "$PROFILE" ]; then
  profile_args=(--profile "$PROFILE")
fi

if ! command -v go >/dev/null 2>&1; then
  echo "Missing prerequisite: go" >&2
  exit 1
fi

if [ "$FORCE_BUILD" = true ] || [ ! -x "$MULTICA_BIN" ]; then
  echo "==> Building CLI -> server/bin/multica"
  make build
fi

if [ ! -x "$MULTICA_BIN" ]; then
  echo "Missing $MULTICA_BIN after build" >&2
  exit 1
fi

# Launch this checkout's binary, not ~/.local/bin/multica. Without this,
# `daemon start` re-execs the installed Computer and agents never see the
# brief compiled in server/bin/multica.
export MULTICA_COMPUTER_LAUNCH_BIN="$MULTICA_BIN"

daemon_cmd=( "$MULTICA_BIN" "${profile_args[@]}" daemon )

stop_running_daemon() {
  # Always ask stop. `daemon/computer status` exits non-zero when the resident
  # is alive but disconnected from the backend (typical after a prod restart),
  # so grepping status with `set -o pipefail` used to skip stop, then start
  # failed with "already running" and start-prod tore down the whole stack.
  echo "==> Stopping running daemon (if any)"
  "${daemon_cmd[@]}" stop
}

if [ "$RESTART" = true ]; then
  stop_running_daemon
fi

if [ "$FOREGROUND" = true ]; then
  echo "==> Starting daemon in foreground ($MULTICA_BIN)"
  exec "${daemon_cmd[@]}" start --foreground
fi

if [ "$RESTART" = true ]; then
  echo "==> Starting daemon ($MULTICA_BIN)"
  echo "    launch binary: $MULTICA_COMPUTER_LAUNCH_BIN"
  "${daemon_cmd[@]}" start
else
  echo "==> Starting daemon ($MULTICA_BIN)"
  if ! "${daemon_cmd[@]}" start; then
    echo "Hint: daemon may already be running; re-run without --no-restart" >&2
    exit 1
  fi
fi

echo ""
# Status exits non-zero while the Computer is still connecting; do not fail
# the start script after a successful launch.
"${daemon_cmd[@]}" status || true
echo ""
echo "Logs:  ${daemon_cmd[*]} logs -f"
echo "Stop:  ${daemon_cmd[*]} stop"
