#!/usr/bin/env bash
# #801 completion criterion ②: alias call-volume zero observation.
#
# Reads multica_agent_surface_human_route_hits_total from a served /metrics
# (or Prometheus) endpoint and prints a three-way verdict:
#   ZERO        — no residual human-path agent traffic in the window
#   RESIDUAL    — hits remain; prints top sites (and agent labels if present)
#   NO_DATA     — metric series missing (not deployed / wrong scrape)
#
# Usage:
#   ./scripts/measure-agent-human-route-hits.sh
#   METRICS_URL=https://host/metrics WINDOW=1h ./scripts/measure-agent-human-route-hits.sh
#   PROM_URL=http://prometheus:9090 WINDOW=1h ./scripts/measure-agent-human-route-hits.sh
#
# Exit codes: 0=ZERO, 2=RESIDUAL, 3=NO_DATA, 1=usage/tool error

set -euo pipefail

WINDOW="${WINDOW:-1h}"
METRICS_URL="${METRICS_URL:-}"
PROM_URL="${PROM_URL:-}"
METRIC='multica_agent_surface_human_route_hits_total'

if ! command -v curl >/dev/null 2>&1; then
  echo "ERROR: curl required" >&2
  exit 1
fi

fetch_from_metrics_text() {
  local url="$1"
  curl -fsS --max-time 30 "$url" || return 1
}

query_prom() {
  local expr="$1"
  local q
  q=$(python3 -c "import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))" "$expr")
  curl -fsS --max-time 30 "${PROM_URL%/}/api/v1/query?query=${q}"
}

verdict_from_prom() {
  local total
  total=$(query_prom "sum(increase(${METRIC}[${WINDOW}]))" \
    | python3 -c '
import json,sys
d=json.load(sys.stdin)
try:
  r=d["data"]["result"]
  if not r:
    print("NO_DATA"); sys.exit(0)
  v=float(r[0]["value"][1])
  print(v)
except Exception:
  print("NO_DATA")
')
  if [[ "$total" == "NO_DATA" ]]; then
    echo "VERDICT: NO_DATA"
    echo "metric ${METRIC} not found or empty for window=${WINDOW}"
    exit 3
  fi
  # float compare
  local is_zero
  is_zero=$(python3 -c "import sys; print('1' if abs(float(sys.argv[1])) < 1e-9 else '0')" "$total")
  if [[ "$is_zero" == "1" ]]; then
    echo "VERDICT: ZERO"
    echo "sum(increase(${METRIC}[${WINDOW}])) = ${total}"
    exit 0
  fi
  echo "VERDICT: RESIDUAL"
  echo "sum(increase(${METRIC}[${WINDOW}])) = ${total}"
  echo "--- top sites ---"
  query_prom "topk(20, sum by (site) (increase(${METRIC}[${WINDOW}])))" \
    | python3 -c '
import json,sys
d=json.load(sys.stdin)
for r in d.get("data",{}).get("result",[]):
  site=r.get("metric",{}).get("site","?")
  v=r.get("value",[None,"0"])[1]
  print(f"  site={site}  increase={v}")
'
  exit 2
}

verdict_from_raw_metrics() {
  local body="$1"
  if ! grep -q "^${METRIC}" <<<"$body" && ! grep -q "^# HELP ${METRIC}" <<<"$body"; then
    echo "VERDICT: NO_DATA"
    echo "metric ${METRIC} not present in ${METRICS_URL}"
    exit 3
  fi
  # Counter scrape is cumulative since process start — report per-site totals.
  echo "NOTE: raw /metrics is process-lifetime counters (not a time window)."
  echo "Prefer PROM_URL for increase[${WINDOW}]. Window arg ignored for raw scrape."
  python3 - "$METRIC" <<<"$body" <<'PY'
import re,sys
metric=sys.argv[1]
text=sys.stdin.read()
# match multica_agent_surface_human_route_hits_total{site="X"} N
pat=re.compile(rf'^{re.escape(metric)}\{{site="([^"]+)"\}}\s+([0-9.eE+-]+)', re.M)
rows=[]
for m in pat.finditer(text):
  rows.append((m.group(1), float(m.group(2))))
# also unlabelled
pat2=re.compile(rf'^{re.escape(metric)}\s+([0-9.eE+-]+)', re.M)
for m in pat2.finditer(text):
  rows.append(("_total", float(m.group(1))))
if not rows:
  print("VERDICT: NO_DATA")
  print(f"metric {metric} HELP present but no samples")
  sys.exit(3)
total=sum(v for _,v in rows if _!="_total")
if total==0 and all(v==0 for _,v in rows):
  print("VERDICT: ZERO")
  print(f"all samples of {metric} are 0 (process lifetime)")
  for s,v in sorted(rows):
    print(f"  site={s}  value={v}")
  sys.exit(0)
print("VERDICT: RESIDUAL")
print(f"process-lifetime sum≈{total}")
for s,v in sorted(rows, key=lambda x:-x[1]):
  if v>0:
    print(f"  site={s}  value={v}")
sys.exit(2)
PY
}

if [[ -n "$PROM_URL" ]]; then
  verdict_from_prom
elif [[ -n "$METRICS_URL" ]]; then
  body=$(fetch_from_metrics_text "$METRICS_URL") || {
    echo "ERROR: failed to fetch METRICS_URL=$METRICS_URL" >&2
    exit 1
  }
  verdict_from_raw_metrics "$body"
else
  cat >&2 <<EOF
Usage:
  PROM_URL=http://prometheus:9090 [WINDOW=1h] $0
  METRICS_URL=https://served-host/metrics $0

Metric: ${METRIC}
EOF
  exit 1
fi
