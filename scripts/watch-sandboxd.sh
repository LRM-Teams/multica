#!/usr/bin/env bash
# Watch ~/.multica/sandbox_daemon/*.json and keep a matching sandboxd process
# running for each config, using this checkout's server/bin/multica.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MULTICA_BIN="${MULTICA_BIN:-$REPO_ROOT/server/bin/multica}"
CONFIG_DIR="${SANDBOXD_CONFIG_DIR:-$HOME/.multica/sandbox_daemon}"
LOG_DIR="${SANDBOXD_LOG_DIR:-$CONFIG_DIR/logs}"
INTERVAL_SEC="${SANDBOXD_WATCH_INTERVAL:-5}"

usage() {
  cat <<'EOF'
Usage: scripts/watch-sandboxd.sh

Scan ~/.multica/sandbox_daemon/*.json every 5s. For any config without a
matching `multica sandboxd --config …` process, start one with
./server/bin/multica from this checkout.

Environment:
  MULTICA_BIN              Override CLI binary (default: <repo>/server/bin/multica)
  SANDBOXD_CONFIG_DIR      Config directory (default: ~/.multica/sandbox_daemon)
  SANDBOXD_LOG_DIR         Log directory (default: <config-dir>/logs)
  SANDBOXD_WATCH_INTERVAL  Scan interval in seconds (default: 5)

Stop with Ctrl+C.
EOF
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
  usage
  exit 0
fi

if [ ! -x "$MULTICA_BIN" ]; then
  echo "Missing executable: $MULTICA_BIN" >&2
  echo "Run: make build" >&2
  exit 1
fi

mkdir -p "$CONFIG_DIR" "$LOG_DIR"

# True when a sandboxd process is already using this config path.
is_running_for_config() {
  local config_path="$1"
  local line
  while IFS= read -r line; do
    case "$line" in
      *watch-sandboxd*) continue ;;
    esac
    case "$line" in
      *sandboxd*)
        case "$line" in
          *"--config ${config_path}"*|*"--config=${config_path}"*)
            return 0
            ;;
        esac
        ;;
    esac
  done < <(ps -eo args=)
  return 1
}

start_sandboxd() {
  local config_path="$1"
  local base log_file
  base="$(basename "$config_path" .json)"
  log_file="$LOG_DIR/${base}.log"

  echo "$(date -Is) starting sandboxd for $config_path (log: $log_file)"
  # setsid detaches from the watcher process group so Ctrl+C / timeout
  # on the watcher does not kill started sandboxd processes.
  setsid "$MULTICA_BIN" sandboxd --config "$config_path" >>"$log_file" 2>&1 < /dev/null &
}

scan_once() {
  local config_path
  shopt -s nullglob
  local configs=("$CONFIG_DIR"/*.json)
  shopt -u nullglob

  if [ ${#configs[@]} -eq 0 ]; then
    return 0
  fi

  for config_path in "${configs[@]}"; do
    if is_running_for_config "$config_path"; then
      continue
    fi
    start_sandboxd "$config_path"
  done
}

echo "Watching $CONFIG_DIR every ${INTERVAL_SEC}s"
echo "Using   $MULTICA_BIN"
echo "Logs in $LOG_DIR"
echo "Press Ctrl+C to stop the watcher (started sandboxd processes keep running)."

while true; do
  scan_once
  sleep "$INTERVAL_SEC"
done
