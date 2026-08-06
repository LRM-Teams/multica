#!/usr/bin/env bash
set -euo pipefail

key=${1:?usage: compose-environment-value.sh <KEY> [DEFAULT]}
has_default=false
default_value=
if (( $# >= 2 )); then
  has_default=true
  default_value=$2
fi
found=false
value=

# Input must be the data-only output of `docker compose config --environment`.
# Never eval/source dotenv content: Compose dotenv syntax is not shell syntax,
# and runtime configuration must not be able to overwrite workflow controls.
while IFS= read -r line || [[ -n "$line" ]]; do
  case "$line" in
    "$key="*)
      if [[ "$found" == true ]]; then
        echo "duplicate Compose environment key: ${key}" >&2
        exit 1
      fi
      value=${line#*=}
      found=true
      ;;
  esac
done

if [[ "$found" != true && "$has_default" == true ]]; then
  value=$default_value
  found=true
fi

if [[ "$found" != true ]]; then
  echo "missing Compose environment key: ${key}" >&2
  exit 1
fi

printf '%s' "$value"
