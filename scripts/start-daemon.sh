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
# computer stop can return before the resident exits (graceful shutdown /
# finishing a task). Wait this long before giving up on a clean stop.
STOP_WAIT_SECONDS="${MULTICA_COMPUTER_STOP_WAIT_SECONDS:-60}"

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

if [ -n "$PROFILE" ]; then
  # Machine-wide Computer ignores --profile; keep config switch for non-computer
  # CLI use only. Default ~/.multica/config.json is what the resident reads.
  echo "Note: --profile $PROFILE is ignored for computer start (machine-wide resident uses ~/.multica/config.json)" >&2
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
# `computer start` re-execs the installed Computer and agents never see the
# brief compiled in server/bin/multica.
export MULTICA_COMPUTER_LAUNCH_BIN="$MULTICA_BIN"

# CLI renamed daemon → computer; keep script name for existing make/docs callers.
daemon_cmd=( "$MULTICA_BIN" computer )

computer_status_json() {
  # status exits non-zero when disconnected; still emit JSON on stdout.
  "${daemon_cmd[@]}" status --output json 2>/dev/null || true
}

computer_is_running() {
  local json
  json="$(computer_status_json)"
  # Match both compact and spaced JSON encodings of status=running|starting.
  printf '%s' "$json" | grep -Eq '"status"[[:space:]]*:[[:space:]]*"(running|starting)"'
}

wait_until_computer_stopped() {
  local waited=0
  if ! computer_is_running; then
    return 0
  fi
  echo "==> Waiting for Computer to finish stopping (up to ${STOP_WAIT_SECONDS}s)"
  while [ "$waited" -lt "$STOP_WAIT_SECONDS" ]; do
    if ! computer_is_running; then
      echo "    Computer stopped after ${waited}s"
      return 0
    fi
    sleep 1
    waited=$((waited + 1))
  done
  echo "Computer is still running after ${STOP_WAIT_SECONDS}s; start may fail with already running" >&2
  return 1
}

stop_running_daemon() {
  # Always ask stop. `daemon/computer status` exits non-zero when the resident
  # is alive but disconnected from the backend (typical after a prod restart),
  # so grepping status with `set -o pipefail` used to skip stop, then start
  # failed with "already running" and start-prod tore down the whole stack.
  #
  # `computer stop` can also return success while shutdown is still in flight
  # ("Computer is still stopping"). Wait until status is no longer running
  # before start, otherwise the first start-daemon.sh attempt fails and only
  # the second run succeeds.
  echo "==> Stopping running daemon (if any)"
  "${daemon_cmd[@]}" stop || true
  wait_until_computer_stopped || true
}

start_daemon() {
  echo "==> Starting daemon ($MULTICA_BIN)"
  echo "    launch binary: $MULTICA_COMPUTER_LAUNCH_BIN"
  if "${daemon_cmd[@]}" start; then
    return 0
  fi
  # Last-chance: stop returned early, process died a moment later, or a
  # lingering pid raced us. One more stop+wait+start avoids the "first run
  # fails, second succeeds" loop.
  echo "==> Start reported failure; retrying stop → wait → start once" >&2
  "${daemon_cmd[@]}" stop || true
  wait_until_computer_stopped || true
  "${daemon_cmd[@]}" start
}

if [ "$RESTART" = true ]; then
  stop_running_daemon
fi

if [ "$FOREGROUND" = true ]; then
  echo "==> Starting daemon in foreground ($MULTICA_BIN)"
  exec "${daemon_cmd[@]}" start --foreground
fi

if [ "$RESTART" = true ]; then
  start_daemon
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
