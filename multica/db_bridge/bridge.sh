#!/usr/bin/env bash
#
# bridge.sh — launch the db_bridge stub + executor in a tmux session, each
# wrapped in a respawn loop so the bridge auto-recovers when a process dies.
#
# Usage:
#   ./bridge.sh start   <side>   # start (or no-op if already running)
#   ./bridge.sh restart <side>   # kill the session and start fresh
#   ./bridge.sh stop    <side>   # kill the tmux session
#   ./bridge.sh status  <side>   # show session/windows + /healthz
#   ./bridge.sh attach  <side>   # attach to the tmux session
#
#   <side> is "areal", "multica", or "leagent" (default: areal).
#
# Each process runs in `while true; do ...; sleep $RESTART_DELAY; done`, so a
# crash of either the stub or executor is restarted independently without
# taking the other down. Stale `claimed` rows are reclaimed automatically by
# bridge_claim_next (see README "Crash recovery"), so restarts are safe.
#
# Sides: areal and multica each run a stub + executor; leagent hosts no stub
# (the gateway group moved to the multica side), so `start leagent` runs the
# executor window only.
set -euo pipefail

# Resolve the directory this script lives in (the db_bridge package dir).
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

CMD="${1:-start}"
SIDE="${2:-areal}"
RESTART_DELAY="${BRIDGE_RESTART_DELAY:-3}"

if [[ "$SIDE" != "areal" && "$SIDE" != "leagent" && "$SIDE" != "multica" ]]; then
  echo "error: side must be 'areal', 'multica', or 'leagent' (got '$SIDE')" >&2
  exit 2
fi

SESSION="db_bridge_${SIDE}"
ENV_FILE="${SCRIPT_DIR}/.env.${SIDE}"

# Stub port differs per side (see README configuration table). leagent hosts no
# stub, so it has no health port.
case "$SIDE" in
  areal)   HEALTH_PORT="${BRIDGE_LEAGENT_STUB_PORT:-9101}" ;;
  multica) HEALTH_PORT="${BRIDGE_GATEWAY_STUB_PORT:-9100}" ;;
  leagent) HEALTH_PORT="" ;;
esac

require_tmux() {
  command -v tmux >/dev/null 2>&1 || { echo "error: tmux is not installed" >&2; exit 1; }
}

session_running() {
  tmux has-session -t "$SESSION" 2>/dev/null
}

# Build the respawn-loop command for a given module. The env file is sourced
# inside the loop so a restarted process always picks up current config.
respawn_cmd() {
  local module="$1" label="$2"
  printf 'cd %q && set -a && source %q && set +a; while true; do echo "[bridge] starting %s --side %s"; uv run python -m %s --side %s; code=$?; echo "[bridge] %s exited ($code), restarting in %ss"; sleep %s; done' \
    "$SCRIPT_DIR" "$ENV_FILE" "$label" "$SIDE" "$module" "$SIDE" "$label" "$RESTART_DELAY" "$RESTART_DELAY"
}

start() {
  require_tmux
  if [[ ! -f "$ENV_FILE" ]]; then
    echo "error: env file not found: $ENV_FILE" >&2
    exit 1
  fi
  if session_running; then
    echo "bridge already running in tmux session '$SESSION' (use 'restart' to recreate)"
    return 0
  fi

  if [[ "$SIDE" == "leagent" ]]; then
    # leagent hosts no stub (gateway moved to the multica side); executor only.
    tmux new-session -d -s "$SESSION" -n executor "$(respawn_cmd db_bridge.run_executor executor)"
    echo "started tmux session '$SESSION' (window: executor; leagent has no stub)"
  else
    tmux new-session -d -s "$SESSION" -n stub     "$(respawn_cmd db_bridge.run_stub     stub)"
    tmux new-window  -t "$SESSION"   -n executor  "$(respawn_cmd db_bridge.run_executor executor)"
    echo "started tmux session '$SESSION' (windows: stub, executor)"
  fi
  echo "  attach: ./bridge.sh attach $SIDE   |   status: ./bridge.sh status $SIDE"
}

stop() {
  require_tmux
  if session_running; then
    tmux kill-session -t "$SESSION"
    echo "stopped tmux session '$SESSION'"
  else
    echo "no tmux session '$SESSION' to stop"
  fi
}

status() {
  require_tmux
  if session_running; then
    echo "session '$SESSION' is running:"
    tmux list-windows -t "$SESSION"
  else
    echo "session '$SESSION' is NOT running"
  fi
  if [[ -n "$HEALTH_PORT" ]]; then
    echo -n "healthz (127.0.0.1:${HEALTH_PORT}): "
    if command -v curl >/dev/null 2>&1; then
      curl -s --max-time 3 "http://127.0.0.1:${HEALTH_PORT}/healthz" || echo "unreachable"
      echo
    else
      echo "curl not installed; skipping"
    fi
  else
    echo "healthz: n/a (side '$SIDE' hosts no stub)"
  fi
}

case "$CMD" in
  start)   start ;;
  stop)    stop ;;
  restart) stop; start ;;
  status)  status ;;
  attach)  require_tmux; tmux attach -t "$SESSION" ;;
  *)
    echo "usage: $0 {start|stop|restart|status|attach} {areal|multica|leagent}" >&2
    exit 2
    ;;
esac
