# Shared port-stop helpers. Source from other scripts:
#   # shellcheck disable=SC1091
#   . "$REPO_ROOT/scripts/lib/stop-port.sh"
#   stop_port_listeners "$PORT" "backend"

pids_on_port() {
  local port="$1"
  local pids=""

  pids="$(lsof -ti:"$port" 2>/dev/null || true)"
  if [ -n "$pids" ]; then
    printf '%s\n' "$pids"
    return 0
  fi

  # lsof often misses node/next listeners on Linux; fuser/ss are reliable fallbacks.
  if command -v fuser >/dev/null 2>&1; then
    pids="$(fuser -n tcp "$port" 2>/dev/null | tr -s ' ' '\n' | grep -E '^[0-9]+$' || true)"
    if [ -n "$pids" ]; then
      printf '%s\n' "$pids"
      return 0
    fi
  fi

  if command -v ss >/dev/null 2>&1; then
    pids="$(
      ss -tlnp "sport = :$port" 2>/dev/null \
        | sed -n 's/.*pid=\([0-9]*\).*/\1/p' \
        | sort -u \
        || true
    )"
    if [ -n "$pids" ]; then
      printf '%s\n' "$pids"
    fi
  fi
}

stop_port_listeners() {
  local port="$1"
  local label="${2:-process}"
  local pids

  pids="$(pids_on_port "$port" | paste -sd' ' - || true)"
  if [ -z "$pids" ]; then
    echo "==> No $label listening on port $port"
    return 0
  fi

  echo "==> Stopping $label on port $port (pid: $pids)"
  # shellcheck disable=SC2086
  kill $pids 2>/dev/null || true

  for _ in 1 2 3 4 5; do
    sleep 0.2
    pids="$(pids_on_port "$port" | paste -sd' ' - || true)"
    [ -z "$pids" ] && return 0
  done

  echo "==> Force stopping remaining $label process(es) on port $port"
  # shellcheck disable=SC2086
  kill -9 $pids 2>/dev/null || true
}
