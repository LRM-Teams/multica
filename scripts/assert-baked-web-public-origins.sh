#!/usr/bin/env bash
# Fail if a served /login page bakes the wrong public API origin into its
# layout chunk. NEXT_PUBLIC_* is compile-time; a healthy /health probe cannot
# catch a test image that still talks to production.
set -euo pipefail

usage() {
  echo "usage: assert-baked-web-public-origins.sh --from-dir <dir> --expect <origin> [--forbid <origin> ...]" >&2
  echo "       assert-baked-web-public-origins.sh --url <login-url> --expect <origin> [--forbid <origin> ...] [--curl-opt <flag> ...]" >&2
  exit 2
}

from_dir=""
login_url=""
expect=""
forbids=()
curl_opts=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --from-dir)
      from_dir=${2:?}
      shift 2
      ;;
    --url)
      login_url=${2:?}
      shift 2
      ;;
    --expect)
      expect=${2:?}
      shift 2
      ;;
    --forbid)
      forbids+=("${2:?}")
      shift 2
      ;;
    --curl-opt)
      curl_opts+=("${2:?}")
      shift 2
      ;;
    *)
      usage
      ;;
  esac
done

[[ -n "$expect" ]] || usage
if [[ -n "$from_dir" && -n "$login_url" ]]; then
  echo "use either --from-dir or --url, not both" >&2
  exit 2
fi
if [[ -z "$from_dir" && -z "$login_url" ]]; then
  usage
fi

if [[ -n "$from_dir" ]]; then
  html_path="$from_dir/login.html"
  if [[ ! -f "$html_path" ]]; then
    echo "missing login.html under $from_dir" >&2
    exit 1
  fi
  html="$(<"$html_path")"
  resolve_chunk() {
    local rel=$1
    local path="${from_dir}${rel}"
    if [[ ! -f "$path" ]]; then
      echo "missing layout chunk: $path" >&2
      exit 1
    fi
    cat "$path"
  }
else
  html="$(curl -fsS --max-time 20 "${curl_opts[@]}" "$login_url")"
  resolve_chunk() {
    local rel=$1
    local base
    base="$(printf '%s' "$login_url" | sed -E 's#(https?://[^/]+).*#\1#')"
    curl -fsS --max-time 20 "${curl_opts[@]}" "${base}${rel}"
  }
fi

chunks="$(printf '%s' "$html" | grep -oE '/_next/static/chunks/app/layout-[^"[:space:]]+\.js' | sort -u || true)"
if [[ -z "$chunks" ]]; then
  echo "login page has no app/layout-*.js chunk" >&2
  exit 1
fi

found_expect=0
chunk_list=""
while IFS= read -r rel; do
  [[ -n "$rel" ]] || continue
  chunk_list="${chunk_list}${chunk_list:+ }${rel}"
  body="$(resolve_chunk "$rel")"
  if grep -Fq -- "$expect" <<<"$body"; then
    found_expect=1
  fi
  for forbidden in "${forbids[@]}"; do
    if grep -Fq -- "$forbidden" <<<"$body"; then
      echo "baked public origin leak: chunk=${rel} contains ${forbidden}" >&2
      exit 1
    fi
  done
done <<<"$chunks"

if [[ "$found_expect" -ne 1 ]]; then
  echo "baked public origin missing: expected ${expect} in ${chunk_list}" >&2
  exit 1
fi

echo "baked public origins OK: expect=${expect} chunks=${chunk_list}"
