#!/usr/bin/env bash
set -euo pipefail

test ! -e .github/workflows/desktop-smoke.yml
test ! -e .github/workflows/mobile-verify.yml

if grep -REn \
  'macos-latest|windows-latest|ubuntu-24\.04-arm|linux/arm64|apps/(desktop|mobile)|goreleaser/' \
  .github/workflows; then
  echo "::error::GitHub Actions must remain Web-service-only"
  exit 1
fi
