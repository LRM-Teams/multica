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
deploy_test_workflow="$(<.github/workflows/deploy-test.yml)"
if grep -Fq -- 'Publish Agent avatar presets to OSS' <<<"$deploy_workflow"; then
  echo "One-time Agent avatar publication must not gate every application deployment"
  exit 1
fi
if grep -Fq -- 'publish_agent_avatar_presets' Dockerfile; then
  echo "The runtime image must not carry the retired one-time Agent avatar publisher"
  exit 1
fi
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

for workflow in "$deploy_workflow" "$deploy_test_workflow" "$release_workflow"; do
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
  'branches: [main]' \
  "github.ref == 'refs/heads/main'" \
  'runs-on: [self-hosted, aliyun]' \
  'url: https://www.leagent.me'; do
  if ! grep -Fq -- "$required" <<<"$deploy_workflow"; then
    echo "Production deployment contract is missing: $required"
    exit 1
  fi
done

for required in \
  'branches: [dev]' \
  "github.ref == 'refs/heads/dev'" \
  'runs-on: [self-hosted, s89, test]' \
  'name: test' \
  'MULTICA_TEST_HOST' \
  'url: ${{ env.MULTICA_APP_URL }}'; do
  if ! grep -Fq -- "$required" <<<"$deploy_test_workflow"; then
    echo "Test deployment contract is missing: $required"
    exit 1
  fi
done

for required in \
  'Resolve the Computer version pinned by test' \
  '"${COMPUTER_FEED_URL}/metainfo.json"' \
  '--retry-all-errors --connect-timeout 10 --max-time 30' \
  "jq -er '.environments.test.tag'" \
  'computer_version="${{ steps.computer_release.outputs.version }}"' \
  'tag=sha-${sha}-computer-${computer_version}' \
  'NEXT_PUBLIC_COMPUTER_VERSION=${{ steps.computer_release.outputs.version }}'; do
  if ! grep -Fq -- "$required" <<<"$deploy_test_workflow"; then
    echo "Test deployment is missing exact Computer release pinning: $required"
    exit 1
  fi
done

if grep -Fq -- 'echo "tag=sha-${sha}"' <<<"$deploy_test_workflow"; then
  echo "Test image tags must include the exact Computer version as well as the source SHA"
  exit 1
fi

if grep -Fq -- 'compose run --rm --no-deps --pull always' <<<"$deploy_test_workflow"; then
  echo "Test migration must not use docker compose run --pull; s89 Compose does not support that flag"
  exit 1
fi

if ! grep -Fq -- 'compose pull backend' <<<"$deploy_test_workflow"; then
  echo "Test deployment must pull the migration image before docker compose run"
  exit 1
fi

if grep -Fq -- 'branches: [dev]' <<<"$deploy_workflow"; then
  echo "Production deployment must not consume dev pushes"
  exit 1
fi

if grep -Fq -- 'branches: [main]' <<<"$deploy_test_workflow"; then
  echo "Test deployment must not consume main pushes"
  exit 1
fi

for required in \
  'publish-downloads-feed:' \
  'OSS_BUCKET: leagent' \
  'RELEASE_PREFIX: computer' \
  'PUBLIC_BASE_URL: https://cdn.leagent.me/computer' \
  '^v[0-9]+\.[0-9]+\.[0-9]+(-(alpha|beta|rc)\.[0-9]+)?$' \
  'canonical_prefix="${RELEASE_PREFIX}/${version}"' \
  'immutable_keys=(' \
  '"${canonical_prefix}/checksums.txt"' \
  'immutable_keys+=("${canonical_prefix}/$(basename "$f")")' \
  's3://${OSS_BUCKET}/${canonical_prefix}/manifest.json' \
  'group: computer-release-feed' \
  'Build canonical environment metainfo' \
  '--retry-all-errors --connect-timeout 10 --max-time 30' \
  's3://${OSS_BUCKET}/${RELEASE_PREFIX}/metainfo.json' \
  'verify_matches "${PUBLIC_BASE_URL}/metainfo.json"' \
  'Remove retired release metadata aliases' \
  'for name in manifest.json latest.json alpha.json' \
  '--include "v*/release.json"' \
  'verify_absent "${PUBLIC_BASE_URL}/${name}"' \
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

if grep -Fq -- 'Ensure release feed directory' .github/workflows/deploy.yml .github/workflows/deploy-test.yml; then
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
