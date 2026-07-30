#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKER="$ROOT_DIR/scripts/assert-served-app-image-provenance.sh"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT

mkdir -p "$TEST_DIR/bin" "$TEST_DIR/deploy"
touch "$TEST_DIR/deploy/.env" "$TEST_DIR/selfhost.yml" "$TEST_DIR/host.yml" "$TEST_DIR/oss.yml"

cat >"$TEST_DIR/bin/docker" <<'DOCKER'
#!/usr/bin/env bash
set -euo pipefail

backend_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
frontend_id=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
backend_ref=ghcr.io/lrm-teams/multica-backend:sha-1234567
frontend_ref=ghcr.io/lrm-teams/multica-web:sha-1234567
backend_image_id=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
frontend_image_id=sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd

if [ "$1" = compose ]; then
  service="${@: -1}"
  case "$service" in
    backend) printf '%s\n' "$backend_id" ;;
    frontend) printf '%s\n' "$frontend_id" ;;
    *) exit 2 ;;
  esac
  exit 0
fi

if [ "$1" = inspect ] && [ "$2" = --format ]; then
  case "$3:$4" in
    '{{.Config.Image}}':"$backend_id") printf '%s\n' "${FAKE_BACKEND_CONFIG_REF:-$backend_ref}" ;;
    '{{.Config.Image}}':"$frontend_id") printf '%s\n' "$frontend_ref" ;;
    '{{.Image}}':"$backend_id") printf '%s\n' "$backend_image_id" ;;
    '{{.Image}}':"$frontend_id") printf '%s\n' "$frontend_image_id" ;;
    *) exit 2 ;;
  esac
  exit 0
fi

if [ "$1" = image ] && [ "$2" = inspect ] && [ "$3" = --format ]; then
  case "$5" in
    "$backend_ref") printf '%s\n' "${FAKE_BACKEND_LOCAL_IMAGE_ID:-$backend_image_id}" ;;
    "$frontend_ref") printf '%s\n' "$frontend_image_id" ;;
    *) exit 2 ;;
  esac
  exit 0
fi

exit 2
DOCKER
chmod +x "$TEST_DIR/bin/docker"

run_checker() {
  PATH="$TEST_DIR/bin:$PATH" bash "$CHECKER" \
    "$@" \
    "$TEST_DIR/deploy" \
    "$TEST_DIR/selfhost.yml" \
    "$TEST_DIR/host.yml" \
    "$TEST_DIR/oss.yml" \
    ghcr.io/lrm-teams/multica-backend:sha-1234567 \
    ghcr.io/lrm-teams/multica-web:sha-1234567
}

success_output="$(run_checker pre-health)"
grep -Fq 'phase=pre-health service=backend' <<<"$success_output"
grep -Fq 'phase=pre-health service=frontend' <<<"$success_output"

if FAKE_BACKEND_CONFIG_REF=ghcr.io/lrm-teams/multica-backend:sha-wrong run_checker pre-health >"$TEST_DIR/config-mismatch" 2>&1; then
  echo "Expected configured image reference mismatch to fail."
  exit 1
fi
grep -Fq 'configured_ref=ghcr.io/lrm-teams/multica-backend:sha-wrong' "$TEST_DIR/config-mismatch"

if FAKE_BACKEND_LOCAL_IMAGE_ID=sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee run_checker post-health >"$TEST_DIR/id-mismatch" 2>&1; then
  echo "Expected immutable image ID mismatch to fail."
  exit 1
fi
grep -Fq 'container_image_id=sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc' "$TEST_DIR/id-mismatch"
grep -Fq 'local_image_id=sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee' "$TEST_DIR/id-mismatch"

echo "served app image provenance checker ok"
