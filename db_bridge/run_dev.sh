#!/usr/bin/env bash
set -uo pipefail

cd "$(dirname "$0")"
mkdir -p logs

: > logs/shell.log
: > logs/bridge.log
echo "[$(date -Is)] run_dev.sh start" | tee -a logs/shell.log logs/bridge.log

set -a && . ./.env.areal && set +a

pids=()
cleanup() {
  trap - INT TERM EXIT
  kill "${pids[@]}" 2>/dev/null || true
  wait "${pids[@]}" 2>/dev/null || true
}
trap cleanup INT TERM EXIT

PYTHON="$(dirname "$0")/.venv/bin/python"
AREAL_REMOTE_SHELL_ENABLED=true "$PYTHON" -m db_bridge.run_shell_runner >> logs/shell.log  2>&1 & pids+=("$!")
# "$PYTHON" -m db_bridge.run_stub     --side areal >> logs/bridge.log 2>&1 & pids+=("$!")
"$PYTHON" -m db_bridge.run_executor --side areal >> logs/bridge.log 2>&1 & pids+=("$!")

wait -n "${pids[@]}"
status=$?
cleanup
exit "$status"
