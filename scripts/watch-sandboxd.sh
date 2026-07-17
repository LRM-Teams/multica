#!/usr/bin/env bash
# Watch ~/.multica/sandbox_daemon/*.json and keep a matching sandboxd process
# running for each config, using this checkout's server/bin/multica.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MULTICA_BIN="${MULTICA_BIN:-$REPO_ROOT/server/bin/multica}"
CONFIG_DIR="${SANDBOXD_CONFIG_DIR:-$HOME/.multica/sandbox_daemon}"
LOG_DIR="${SANDBOXD_LOG_DIR:-$CONFIG_DIR/logs}"
INTERVAL_SEC="${SANDBOXD_WATCH_INTERVAL:-5}"

declare -A CONFIG_FINGERPRINTS=()

usage() {
  cat <<'EOF'
Usage: scripts/watch-sandboxd.sh

Scan ~/.multica/sandbox_daemon/*.json every 5s. For any config without a
matching `multica sandboxd --config …` process, start one with
./server/bin/multica from this checkout. When a config file's contents change,
stop the matching sandboxd and start a fresh one.

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

# SHA-256 of config contents; used to detect edits between scans.
# Ignore cube_template_id: sandboxd persists control-plane template changes
# into this file, and restarting on that write caused needless offline flaps
# (the process already hot-applies the new template in memory).
config_fingerprint() {
  local config_path="$1"
  python3 - "$config_path" <<'PY'
import hashlib, json, sys

path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    obj = json.load(f)
if isinstance(obj, dict):
    obj.pop("cube_template_id", None)
raw = json.dumps(obj, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode("utf-8")
print(hashlib.sha256(raw).hexdigest())
PY
}

# Print PIDs of sandboxd processes bound to this config path.
pids_for_config() {
  local config_path="$1"
  local pid args
  while read -r pid args; do
    case "$args" in
      *watch-sandboxd*) continue ;;
      *sandboxd*)
        case "$args" in
          *"--config ${config_path}"*|*"--config=${config_path}"*)
            echo "$pid"
            ;;
        esac
        ;;
    esac
  done < <(ps -eo pid=,args=)
}

# True when a sandboxd process is already using this config path.
is_running_for_config() {
  local config_path="$1"
  local pid
  while IFS= read -r pid; do
    if [ -n "$pid" ]; then
      return 0
    fi
  done < <(pids_for_config "$config_path")
  return 1
}

stop_sandboxd() {
  local config_path="$1"
  local pid
  local -a pids=()

  while IFS= read -r pid; do
    if [ -n "$pid" ]; then
      pids+=("$pid")
    fi
  done < <(pids_for_config "$config_path")

  if [ ${#pids[@]} -eq 0 ]; then
    return 0
  fi

  echo "$(date -Is) stopping sandboxd for $config_path (pids: ${pids[*]})"
  kill "${pids[@]}" 2>/dev/null || true

  local attempt
  for attempt in $(seq 1 10); do
    pids=()
    while IFS= read -r pid; do
      if [ -n "$pid" ]; then
        pids+=("$pid")
      fi
    done < <(pids_for_config "$config_path")
    if [ ${#pids[@]} -eq 0 ]; then
      return 0
    fi
    sleep 0.5
  done

  echo "$(date -Is) force killing sandboxd for $config_path (pids: ${pids[*]})"
  kill -9 "${pids[@]}" 2>/dev/null || true
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

restart_sandboxd() {
  local config_path="$1"
  stop_sandboxd "$config_path"
  start_sandboxd "$config_path"
}

scan_once() {
  local config_path fingerprint previous
  shopt -s nullglob
  local configs=("$CONFIG_DIR"/*.json)
  shopt -u nullglob

  if [ ${#configs[@]} -eq 0 ]; then
    return 0
  fi

  for config_path in "${configs[@]}"; do
    fingerprint="$(config_fingerprint "$config_path")"
    previous="${CONFIG_FINGERPRINTS["$config_path"]:-}"

    if [ -z "$previous" ]; then
      CONFIG_FINGERPRINTS["$config_path"]="$fingerprint"
      if is_running_for_config "$config_path"; then
        echo "$(date -Is) tracking existing sandboxd for $config_path"
        continue
      fi
      start_sandboxd "$config_path"
      continue
    fi

    if [ "$fingerprint" = "$previous" ]; then
      if ! is_running_for_config "$config_path"; then
        start_sandboxd "$config_path"
      fi
      continue
    fi

    echo "$(date -Is) config changed for $config_path, restarting sandboxd"
    CONFIG_FINGERPRINTS["$config_path"]="$fingerprint"
    restart_sandboxd "$config_path"
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


#   nohup /home/jian40/multica/scripts/watch-sandboxd.sh   >> ~/.multica/sandbox_daemon/logs/watch-sandboxd.log 2>&1 &