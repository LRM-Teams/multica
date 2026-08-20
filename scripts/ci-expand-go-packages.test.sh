#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EXPAND="$ROOT_DIR/scripts/ci-expand-go-packages.sh"

fail() {
  echo "$1" >&2
  exit 1
}

full="$(GO_MODE=full "$EXPAND")"
if [[ "$full" != "./..." ]]; then
  fail "GO_MODE=full must print ./... (got: $full)"
fi

empty="$(GO_SEED_PACKAGES="" "$EXPAND")"
if [[ -n "$empty" ]]; then
  fail "empty seeds must print nothing (got: $empty)"
fi

# protocol is a leaf-ish library; the closure must include the seed itself
# and at least one importer (daemon/handler historically depend on it).
expanded="$(GO_SEED_PACKAGES=$'./pkg/protocol\n' "$EXPAND")"
if [[ "$expanded" != *"./pkg/protocol"* ]]; then
  fail "closure must keep the seed ./pkg/protocol (got: $expanded)"
fi
if [[ "$expanded" == "./pkg/protocol " || "$expanded" == "./pkg/protocol" ]]; then
  fail "closure must include at least one importer of ./pkg/protocol (got: $expanded)"
fi

echo "ci-expand-go-packages ok"
