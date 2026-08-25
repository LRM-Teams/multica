#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKER="$ROOT_DIR/scripts/assert-baked-web-public-origins.sh"
FIXTURE="$(mktemp -d)"
trap 'rm -rf "$FIXTURE"' EXIT

mkdir -p "$FIXTURE/ok/_next/static/chunks/app" "$FIXTURE/bad/_next/static/chunks/app"

ok_chunk="_next/static/chunks/app/layout-ok.js"
bad_chunk="_next/static/chunks/app/layout-bad.js"

cat >"$FIXTURE/ok/login.html" <<EOF
<script src="/${ok_chunk}"></script>
EOF
printf 'apiBaseUrl:"https://82.157.184.89",appUrl:"https://82.157.184.89",environment:"test"\n' \
  >"$FIXTURE/ok/${ok_chunk}"

cat >"$FIXTURE/bad/login.html" <<EOF
<script src="/${bad_chunk}"></script>
EOF
printf 'apiBaseUrl:"https://api.leagent.me",appUrl:"https://www.leagent.me",environment:"production"\n' \
  >"$FIXTURE/bad/${bad_chunk}"

if ! bash "$CHECKER" \
  --from-dir "$FIXTURE/ok" \
  --expect "https://82.157.184.89" \
  --forbid "https://api.leagent.me"; then
  echo "Test-baked login chunk must pass."
  exit 1
fi

if bash "$CHECKER" \
  --from-dir "$FIXTURE/bad" \
  --expect "https://82.157.184.89" \
  --forbid "https://api.leagent.me" >/dev/null 2>&1; then
  echo "Production-baked login chunk must fail."
  exit 1
fi

echo "assert-baked-web-public-origins ok"
