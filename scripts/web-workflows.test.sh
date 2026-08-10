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
deploy_workflow="$(<.github/workflows/deploy.yml)"
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

for workflow in "$deploy_workflow" "$release_workflow"; do
  for required_public_setting in \
    'NEXT_PUBLIC_APP_URL=${{ env.MULTICA_APP_URL }}' \
    'NEXT_PUBLIC_API_URL=${{ env.MULTICA_API_URL }}' \
    'NEXT_PUBLIC_WS_URL=${{ env.MULTICA_WS_URL }}'; do
    if ! grep -Fq -- "$required_public_setting" <<<"$workflow"; then
      echo "Web build is missing environment-specific public endpoint: $required_public_setting"
      exit 1
    fi
  done
done

for required in \
  'publish-downloads-feed:' \
  'OSS_BUCKET: leagent' \
  'RELEASE_PREFIX: computer' \
  'PUBLIC_BASE_URL: https://cdn.leagent.me/computer' \
  '^v[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc)\.[0-9]+)?$' \
  'canonical_prefix="${RELEASE_PREFIX}/${version}"' \
  'legacy_prefix="${RELEASE_PREFIX}/${TAG_NAME}"' \
  'immutable_keys=(' \
  '"${canonical_prefix}/checksums.txt"' \
  'immutable_keys+=("${canonical_prefix}/$(basename "$f")")' \
  '"${canonical_prefix}/manifest.json" "${legacy_prefix}/release.json"' \
  'for name in manifest.json latest.json' \
  "if: env.IS_STABLE != 'true'" \
  's3://${OSS_BUCKET}/${RELEASE_PREFIX}/alpha.json' \
  'verify_matches "${PUBLIC_BASE_URL}/alpha.json"' \
  'aliyun --config-path "$config" \' \
  'cdn RefreshObjectCaches' \
  'Verify the published feed through the public CDN'; do
  if ! grep -Fq -- "$required" <<<"$release_workflow"; then
    echo "Canonical CDN release contract is missing: $required"
    exit 1
  fi
done

if grep -Fq -- '--profile release-cdn' <<<"$release_workflow"; then
  echo "Aliyun CLI cannot create a named profile with configure set in a fresh config file"
  exit 1
fi

if grep -Fq -- 'runs-on: [self-hosted, aliyun]' <<<"$release_workflow"; then
  echo "Release publishing must not depend on the Aliyun host runner"
  exit 1
fi

for legacy_marker in \
  'publish-downloads-feed-oss' \
  'lrm-2-0-release' \
  '/data/multica/releases' \
  '/srv/multica-releases'; do
  matches="$(git grep -n -F -- "$legacy_marker" -- ':!scripts/web-workflows.test.sh' || true)"
  if [[ -n "$matches" ]]; then
    echo "Legacy release path remains in the repository: $legacy_marker"
    echo "$matches"
    exit 1
  fi
done

if grep -Fq -- 'cdn.leagent.me' deploy/aliyun/Caddyfile; then
  echo "Caddy must not serve the CDN release feed"
  exit 1
fi

if grep -Fq -- 'multica-releases' docker-compose.aliyun.yml; then
  echo "Aliyun Compose must not mount a local release feed"
  exit 1
fi

if grep -Fq -- 'Ensure release feed directory' .github/workflows/deploy.yml; then
  echo "Deploy workflow must not prepare the removed local release feed"
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
