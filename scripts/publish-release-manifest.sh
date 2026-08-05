#!/usr/bin/env bash
# Builds the release manifest JSON consumed by server/internal/cli/update.go
# (ReleaseManifest) from a GoReleaser checksums.txt, and prints it to stdout.
# Used by the release workflow's publish-downloads-feed job. During the naming
# migration, the same bytes are published under both canonical manifest.json
# paths and the previous release.json/latest.json paths.
#
# Usage: publish-release-manifest.sh <tag> <version> <base-url> <checksums-file> <archive-dir>
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo "Usage: $0 <tag> <version> <base-url> <checksums-file> <archive-dir>" >&2
  exit 1
fi

tag="$1"
version="$2"
base_url="$3"
checksums_file="$4"
archive_dir="$5"

if [ ! -f "$checksums_file" ]; then
  echo "checksums file not found: $checksums_file" >&2
  exit 1
fi

# Only the "versioned" GoReleaser archive id (multica-cli-{version}-{os}-{arch})
# is published here — the "legacy" multica_{os}_{arch} archive exists solely
# for CLIs built before this manifest existed and self-updating via the old
# GitHub-asset-name scheme; it is not part of this feed's contract.
platforms="{}"
while read -r sha filename; do
  [ -z "${filename:-}" ] && continue
  case "$filename" in
    multica-cli-*-darwin-amd64.tar.gz) key="darwin-amd64" ;;
    multica-cli-*-darwin-arm64.tar.gz) key="darwin-arm64" ;;
    multica-cli-*-linux-amd64.tar.gz)  key="linux-amd64" ;;
    multica-cli-*-linux-arm64.tar.gz)  key="linux-arm64" ;;
    multica-cli-*-windows-amd64.zip)   key="windows-amd64" ;;
    multica-cli-*-windows-arm64.zip)   key="windows-arm64" ;;
    *) continue ;;
  esac
  if [ ! -f "$archive_dir/$filename" ]; then
    echo "checksummed archive missing from $archive_dir: $filename" >&2
    exit 1
  fi
  url="${base_url}/${filename}"
  sha_lower="$(printf '%s' "$sha" | tr '[:upper:]' '[:lower:]')"
  platforms=$(jq --arg k "$key" --arg url "$url" --arg sha "$sha_lower" \
    '.[$k] = {"url": $url, "sha256": $sha}' <<<"$platforms")
done < <(awk '{print $1, $2}' "$checksums_file")

if [ "$platforms" = "{}" ]; then
  echo "no recognized platform archives found in $checksums_file" >&2
  exit 1
fi

jq -n --arg tag "$tag" --arg version "$version" --argjson platforms "$platforms" \
  '{tag: $tag, version: $version, platforms: $platforms}'
