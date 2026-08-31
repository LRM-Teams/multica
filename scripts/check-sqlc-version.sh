#!/usr/bin/env bash
# Verify the installed sqlc binary matches the version pinned in server/.sqlc-version.
#
# Invoked by `make sqlc` so regeneration is reproducible: a silent version bump
# of sqlc is the root cause of large unrelated generated diffs (see LRM-1521).
# Respects SQLC_BYPASS_VERSION_CHECK=1 for one-off runs against an intentionally
# pinned identical output.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PIN_FILE="$ROOT_DIR/server/.sqlc-version"

if [[ "${SQLC_BYPASS_VERSION_CHECK:-}" == "1" ]]; then
  echo "sqlc version check skipped (SQLC_BYPASS_VERSION_CHECK=1)"
  exit 0
fi

if [[ ! -f "$PIN_FILE" ]]; then
  echo "error: missing sqlc version pin at server/.sqlc-version" >&2
  exit 1
fi

pinned="$(tr -d '[:space:]' < "$PIN_FILE")"

if ! command -v sqlc >/dev/null 2>&1; then
  echo "error: 'sqlc' binary not found on PATH (pinned version $pinned)" >&2
  exit 1
fi

installed="$(sqlc version | awk '{print $NF}' | tr -d '[:space:]')"

# Normalize a leading 'v' so both '1.31.1' and 'v1.31.1' compare cleanly.
pinned_norm="${pinned#v}"
installed_norm="${installed#v}"

if [[ "$pinned_norm" != "$installed_norm" ]]; then
  echo "error: sqlc version mismatch: pinned $pinned, installed '$installed'" >&2
  echo "  Install sqlc $pinned (see server/.sqlc-version), or set SQLC_BYPASS_VERSION_CHECK=1 to override." >&2
  exit 1
fi

echo "ok: sqlc $installed matches pinned $pinned"
