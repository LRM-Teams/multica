#!/usr/bin/env bash
# Cross-compile the Multica CLI/daemon binary for Windows.
# Same entrypoint as make build (server/cmd/multica); CGO disabled like goreleaser.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

ARCH="amd64"
OUT_DIR="$REPO_ROOT/server/bin"

usage() {
  cat <<'EOF'
Usage: scripts/build-daemon-windows.sh [options]

Cross-compile server/cmd/multica for Windows (daemon + CLI in one binary).

Options:
  --arch amd64|arm64   Target GOARCH (default: amd64)
  --out DIR            Output directory (default: server/bin)
  -h, --help           Show this help

Examples:
  scripts/build-daemon-windows.sh
  scripts/build-daemon-windows.sh --arch arm64
  scripts/build-daemon-windows.sh --out /tmp/multica-win
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --arch)
      shift
      ARCH="${1:?--arch requires a value}"
      ;;
    --out)
      shift
      OUT_DIR="${1:?--out requires a value}"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
  shift
done

case "$ARCH" in
  amd64|arm64) ;;
  *)
    echo "Unsupported --arch: $ARCH (want amd64 or arm64)" >&2
    exit 1
    ;;
esac

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
OUT_FILE="$OUT_DIR/multica-windows-${ARCH}.exe"

mkdir -p "$OUT_DIR"

echo "Building Windows daemon/CLI → $OUT_FILE"
echo "  version=$VERSION commit=$COMMIT date=$DATE arch=$ARCH"

(
  cd "$REPO_ROOT/server"
  CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o "$OUT_FILE" \
    ./cmd/multica
)

ls -lh "$OUT_FILE"
echo "Done. Copy to the Windows machine and run e.g.:"
echo "  .\\$(basename "$OUT_FILE") computer start"
echo "  # or: multica daemon / computer restart (same binary as install)"
