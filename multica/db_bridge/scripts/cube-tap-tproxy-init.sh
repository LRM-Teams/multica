#!/usr/bin/env bash
# Extend Cube TRANSPROXY rules to sandbox tap interfaces (z192.168.0.*).
#
# Cube's default cube-proxy-iptables-init.sh only matches iif cube-dev, but
# microVM sandboxes egress via per-sandbox tap devices.
#
# For db_bridge (:9100/:9101/:9102), tap TPROXY alone is not enough: the from_cube
# BPF program on each tap SNATs allowOut traffic to the host LAN IP and hairpins
# it out the node uplink (eno1np0) before iptables PREROUTING on the tap runs.
# Docker's DOCKER DNAT then forwards to the published container IP and the
# kernel RSTs the SYN-ACK. We DNAT hairpinned bridge ports on the uplink
# directly to 127.0.0.1:<port> (before the DOCKER chain) so the stub answers.
#
# Use the host LAN IP in allowOut (e.g. 10.110.158.143/32), not 192.168.0.1 —
# hairpin to cube-dev is still broken for these bridge ports.
#
# Usage:
#   sudo scripts/cube-tap-tproxy-init.sh up
#   sudo scripts/cube-tap-tproxy-init.sh down
#   sudo scripts/cube-tap-tproxy-init.sh status
#
# Override bridge ports (space- or comma-separated):
#   CUBE_TPROXY_BRIDGE_PORTS="9100 9101 9102" sudo scripts/cube-tap-tproxy-init.sh up
set -euo pipefail

TPROXY_ON_IP="${CUBE_TPROXY_ON_IP:-192.168.0.1}"
TPROXY_PORT_HTTP=8080
TPROXY_PORT_HTTPS=8443
# Keep CUBE_TPROXY_BRIDGE_PORT as a single-port override for older callers.
if [[ -n "${CUBE_TPROXY_BRIDGE_PORTS:-}" ]]; then
  TPROXY_BRIDGE_PORTS_RAW="${CUBE_TPROXY_BRIDGE_PORTS}"
elif [[ -n "${CUBE_TPROXY_BRIDGE_PORT:-}" ]]; then
  TPROXY_BRIDGE_PORTS_RAW="${CUBE_TPROXY_BRIDGE_PORT}"
else
  TPROXY_BRIDGE_PORTS_RAW="9100 9101 9102"
fi
TPROXY_BRIDGE_PORTS_RAW="${TPROXY_BRIDGE_PORTS_RAW//,/ }"
# shellcheck disable=SC2206
TPROXY_BRIDGE_PORTS=(${TPROXY_BRIDGE_PORTS_RAW})
BRIDGE_ON_IP="${CUBE_TPROXY_BRIDGE_ON_IP:-127.0.0.1}"
EGRESS_IFACE="${CUBE_EGRESS_IFACE:-eno1np0}"
HAIRPIN_CHAIN="CUBE_TAP_BRIDGE_HAIRPIN"
ROUTE_TABLE=100
CHAIN="TRANSPROXY"
MARK_CHAIN="CUBE_TAP_TPROXY"

log()   { printf '[tap-tproxy] %s\n' "$*" >&2; }
fatal() { log "FATAL: $*"; exit 1; }

require_root() { [[ "$(id -u)" -eq 0 ]] || fatal "must run as root"; }

iface_sysctl_key() {
  # Linux encodes dotted ifnames as net.ipv4.conf.z192/168/0/101.*
  local iface="$1"
  local key="${iface//./\/}"
  printf 'net.ipv4.conf.%s' "${key}"
}

host_lan_ips() {
  if [[ -n "${CUBE_NODE_IP:-}" ]]; then
    # shellcheck disable=SC2206
    local parts=(${CUBE_NODE_IP//,/ })
    printf '%s\n' "${parts[@]}"
    return
  fi
  ip -o -4 addr show dev "${EGRESS_IFACE}" scope global 2>/dev/null \
    | awk '{print $4}' | cut -d/ -f1
}

add_hairpin_bridge_rules() {
  local ip port
  while IFS= read -r ip; do
    [[ -n "${ip}" ]] || continue
    for port in "${TPROXY_BRIDGE_PORTS[@]}"; do
      [[ -n "${port}" ]] || continue
      if ! iptables -t nat -C "${HAIRPIN_CHAIN}" -i "${EGRESS_IFACE}" -p tcp \
           -d "${ip}" --dport "${port}" \
           -j DNAT --to-destination "${BRIDGE_ON_IP}:${port}" 2>/dev/null; then
        iptables -t nat -A "${HAIRPIN_CHAIN}" -i "${EGRESS_IFACE}" -p tcp \
          -d "${ip}" --dport "${port}" \
          -j DNAT --to-destination "${BRIDGE_ON_IP}:${port}"
        log "hairpin DNAT ${EGRESS_IFACE} ${ip}:${port} -> ${BRIDGE_ON_IP}:${port}"
      fi
    done
  done < <(host_lan_ips)
}

remove_hairpin_bridge_rules() {
  iptables -t nat -F "${HAIRPIN_CHAIN}" 2>/dev/null || true
  while iptables -t nat -C PREROUTING -j "${HAIRPIN_CHAIN}" 2>/dev/null; do
    iptables -t nat -D PREROUTING -j "${HAIRPIN_CHAIN}" || break
  done
  iptables -t nat -X "${HAIRPIN_CHAIN}" 2>/dev/null || true
}

ensure_hairpin_bridge_chain() {
  iptables -t nat -N "${HAIRPIN_CHAIN}" 2>/dev/null || true
  if ! iptables -t nat -C PREROUTING -j "${HAIRPIN_CHAIN}" 2>/dev/null; then
    # Jump before DOCKER so we hit the host stub, not the published container IP.
    if iptables -t nat -C PREROUTING -j DOCKER 2>/dev/null; then
      iptables -t nat -I PREROUTING 1 -j "${HAIRPIN_CHAIN}"
    else
      iptables -t nat -A PREROUTING -j "${HAIRPIN_CHAIN}"
    fi
  fi
  add_hairpin_bridge_rules
}

tap_ifaces() {
  if [[ -n "${CUBE_TAP_IFACE:-}" ]]; then
    echo "${CUBE_TAP_IFACE}"
    return
  fi
  if [[ -d /data/cubelet/network-agent/state ]]; then
    python3 - <<'PY'
import glob, json
seen = set()
for path in sorted(glob.glob("/data/cubelet/network-agent/state/*.json")):
    try:
        data = json.load(open(path, encoding="utf-8"))
    except Exception:
        continue
    tap = data.get("tapName") or data.get("persistMetadata", {}).get("host_tap_name")
    if tap and tap not in seen:
        seen.add(tap)
        print(tap)
PY
    return
  fi
  ip -o link show up | awk -F': ' '{print $2}' | grep -E '^z192\.168\.(0|1)\.' || true
}

ensure_base_chain() {
  iptables -t mangle -N "${CHAIN}" 2>/dev/null || true
  iptables -t mangle -C PREROUTING -j "${CHAIN}" 2>/dev/null \
    || iptables -t mangle -A PREROUTING -j "${CHAIN}"
  if ! ip route show table "${ROUTE_TABLE}" | grep -qE 'local (default|0\.0\.0\.0/0) dev lo'; then
    ip route add local 0.0.0.0/0 dev lo table "${ROUTE_TABLE}" 2>/dev/null \
      || ip route add local default dev lo table "${ROUTE_TABLE}" 2>/dev/null \
      || true
  fi
}

rule_exists() {
  local iface="$1" port="$2"
  ip rule show | grep -q "iif ${iface} ipproto tcp dport ${port} lookup ${ROUTE_TABLE}"
}

add_tap_rules() {
  local iface="$1"
  local port
  ensure_base_chain

  if ! iptables -t mangle -C "${CHAIN}" -i "${iface}" -p tcp --dport 80 \
       -j TPROXY --on-ip "${TPROXY_ON_IP}" --on-port "${TPROXY_PORT_HTTP}" 2>/dev/null; then
    iptables -t mangle -A "${CHAIN}" -i "${iface}" -p tcp --dport 80 \
      -j TPROXY --on-ip "${TPROXY_ON_IP}" --on-port "${TPROXY_PORT_HTTP}"
  fi
  if ! iptables -t mangle -C "${CHAIN}" -i "${iface}" -p tcp --dport 443 \
       -j TPROXY --on-ip "${TPROXY_ON_IP}" --on-port "${TPROXY_PORT_HTTPS}" 2>/dev/null; then
    iptables -t mangle -A "${CHAIN}" -i "${iface}" -p tcp --dport 443 \
      -j TPROXY --on-ip "${TPROXY_ON_IP}" --on-port "${TPROXY_PORT_HTTPS}"
  fi
  for port in "${TPROXY_BRIDGE_PORTS[@]}"; do
    [[ -n "${port}" ]] || continue
    if ! iptables -t mangle -C "${CHAIN}" -i "${iface}" -p tcp --dport "${port}" \
         -j TPROXY --on-ip "${BRIDGE_ON_IP}" --on-port "${port}" 2>/dev/null; then
      iptables -t mangle -A "${CHAIN}" -i "${iface}" -p tcp --dport "${port}" \
        -j TPROXY --on-ip "${BRIDGE_ON_IP}" --on-port "${port}"
    fi
  done

  for port in 80 443 "${TPROXY_BRIDGE_PORTS[@]}"; do
    [[ -n "${port}" ]] || continue
    if ! rule_exists "${iface}" "${port}"; then
      ip rule add iif "${iface}" ipproto tcp dport "${port}" table "${ROUTE_TABLE}" 2>/dev/null \
        || rule_exists "${iface}" "${port}" \
        || log "WARN: could not add ip rule for ${iface} dport ${port}"
    fi
  done
}

remove_tap_rules() {
  local iface="$1"
  local port
  for port in 80 443 "${TPROXY_BRIDGE_PORTS[@]}"; do
    [[ -n "${port}" ]] || continue
    while rule_exists "${iface}" "${port}"; do
      ip rule del iif "${iface}" ipproto tcp dport "${port}" table "${ROUTE_TABLE}" || break
    done
  done
  for port in 80 443; do
    local on_ip on_port
    if [[ "${port}" == "80" ]]; then
      on_ip="${TPROXY_ON_IP}"; on_port="${TPROXY_PORT_HTTP}"
    else
      on_ip="${TPROXY_ON_IP}"; on_port="${TPROXY_PORT_HTTPS}"
    fi
    while iptables -t mangle -C "${CHAIN}" -i "${iface}" -p tcp --dport "${port}" \
          -j TPROXY --on-ip "${on_ip}" --on-port "${on_port}" 2>/dev/null; do
      iptables -t mangle -D "${CHAIN}" -i "${iface}" -p tcp --dport "${port}" \
        -j TPROXY --on-ip "${on_ip}" --on-port "${on_port}" || break
    done
  done
  for port in "${TPROXY_BRIDGE_PORTS[@]}"; do
    [[ -n "${port}" ]] || continue
    while iptables -t mangle -C "${CHAIN}" -i "${iface}" -p tcp --dport "${port}" \
          -j TPROXY --on-ip "${BRIDGE_ON_IP}" --on-port "${port}" 2>/dev/null; do
      iptables -t mangle -D "${CHAIN}" -i "${iface}" -p tcp --dport "${port}" \
        -j TPROXY --on-ip "${BRIDGE_ON_IP}" --on-port "${port}" || break
    done
  done
}

apply_sysctls() {
  # Hairpin DNAT rewrites allowOut bridge ports to 127.0.0.1:<port> on the uplink.
  # Interface-scoped route_localnet must be 1 on the egress NIC; all.* alone
  # does not enable local delivery for packets arriving on eno1np0.
  sysctl -wq net.ipv4.conf.all.rp_filter=0 \
              net.ipv4.conf.all.route_localnet=1 \
              net.ipv4.conf.all.accept_local=1 \
              net.ipv4.conf.lo.rp_filter=0 \
              net.ipv4.conf.lo.route_localnet=1 || true
  local iface key
  for iface in cube-dev "${EGRESS_IFACE}" $(tap_ifaces); do
    key="$(iface_sysctl_key "${iface}")"
    sysctl -wq "${key}.rp_filter=0" \
                "${key}.accept_local=1" \
                "${key}.route_localnet=1" 2>/dev/null || true
  done
}

show_status() {
  log "=== host LAN IPs (hairpin bridge) ==="
  host_lan_ips | sed 's/^/  /' || log "  (none)"
  log "=== bridge ports ==="
  printf '  %s\n' "${TPROXY_BRIDGE_PORTS[*]}"
  log "=== route_localnet (needed for hairpin -> 127.0.0.1) ==="
  sysctl -n net.ipv4.conf.all.route_localnet \
            "net.ipv4.conf.${EGRESS_IFACE//.//}.route_localnet" 2>/dev/null \
    | paste -d' ' - - \
    | awk -v iface="${EGRESS_IFACE}" '{print "  all="$1"  "iface"="$2}' \
    || true
  log "=== nat/${HAIRPIN_CHAIN} ==="
  iptables -t nat -L "${HAIRPIN_CHAIN}" -n -v --line-numbers 2>/dev/null \
    || log "  (chain absent)"
  log "=== tap interfaces ==="
  tap_ifaces | sed 's/^/  /' || log "  (none)"
  log "=== mangle/${CHAIN} (tap rules) ==="
  iptables -t mangle -L "${CHAIN}" -n -v --line-numbers 2>/dev/null \
    | grep -E 'z192\.168|^Chain|^num' || log "  (no tap rules)"
  log "=== ip rule table ${ROUTE_TABLE} (tap) ==="
  ip rule show | grep "lookup ${ROUTE_TABLE}" || true
}

main() {
  local action="${1:-up}"
  case "${action}" in
    up)
      require_root
      apply_sysctls
      ensure_hairpin_bridge_chain
      local iface
      for iface in $(tap_ifaces); do
        log "installing tap TPROXY rules on ${iface}"
        add_tap_rules "${iface}"
      done
      log "tap TPROXY + hairpin bridge rules installed for ports: ${TPROXY_BRIDGE_PORTS[*]}"
      show_status
      ;;
    down)
      require_root
      remove_hairpin_bridge_rules
      local iface
      for iface in $(tap_ifaces); do
        remove_tap_rules "${iface}"
      done
      log "tap TPROXY + hairpin bridge rules removed"
      ;;
    status)
      show_status
      ;;
    *)
      echo "usage: $0 {up|down|status}" >&2
      exit 2
      ;;
  esac
}

main "$@"
