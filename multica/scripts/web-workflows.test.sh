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
# CLI/daemon archives are the sole non-web release lane retained after #1405:
# Frank's Apple Silicon host needs the Darwin arm64 archive to upgrade Wendy.
# Keep the server-image ARM runners disabled below.
for forbidden in \
  'linux/arm64' \
  'ubuntu-24.04-arm'; do
  if grep -Fq -- "$forbidden" <<<"$release_workflow"; then
    echo "Non-web release lane remains enabled: $forbidden"
    exit 1
  fi
done

if ! grep -Fq -- 'goreleaser/goreleaser-action' <<<"$release_workflow"; then
  echo "Daemon CLI release job must stay enabled"
  exit 1
fi

goreleaser_config="$(<.goreleaser.yml)"
if ! perl -0ne 'exit(!/- id: multica-darwin-arm64\n.*?goos:\s*\n\s*- darwin.*?goarch:\s*\n\s*- arm64/s)' <<<"$goreleaser_config"; then
  echo "Darwin arm64 daemon CLI target must stay enabled"
  exit 1
fi

if ! perl -0ne 'exit(!/- id: multica-linux-arm64\n.*?goos:\s*\n\s*- linux.*?goarch:\s*\n\s*- arm64/s)' <<<"$goreleaser_config"; then
  echo "Linux arm64 daemon CLI target must stay enabled"
  exit 1
fi

echo "release workflow policy ok"
