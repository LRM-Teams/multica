#!/usr/bin/env bash
set -euo pipefail

for disabled_workflow in \
  .github/workflows/desktop-smoke.yml \
  .github/workflows/mobile-verify.yml; do
  if [[ -e "$disabled_workflow" ]]; then
    echo "Non-web workflow must stay disabled: $disabled_workflow"
    exit 1
  fi
done

release_workflow="$(<.github/workflows/release.yml)"
for forbidden in \
  'goreleaser/goreleaser-action' \
  'linux/arm64' \
  'ubuntu-24.04-arm'; do
  if grep -Fq -- "$forbidden" <<<"$release_workflow"; then
    echo "Non-web release lane remains enabled: $forbidden"
    exit 1
  fi
done

echo "web-only workflow policy ok"
