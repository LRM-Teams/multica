#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
RUNNER="$ROOT_DIR/scripts/run-aliyun-step-over-ssh.sh"
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT

cat >"$test_root/ssh" <<'EOF'
#!/usr/bin/env bash
cat
EOF
cat >"$test_root/sshpass" <<'EOF'
#!/usr/bin/env bash
test "$1" = -e
shift
exec "$@"
EOF
chmod +x "$test_root/ssh" "$test_root/sshpass"
printf 'printf "remote script ran\\n"\n' >"$test_root/step.sh"

output=$(
  PATH="$test_root:$PATH" \
    SSH_HOST=production.example \
    SSH_USER=root \
    SSH_PASSWORD='password;safe' \
    SSH_KNOWN_HOSTS_PATH=/tmp/known_hosts \
    REMOTE_RUNNER_TEMP='/tmp/deploy path' \
    IMAGE_TAG='sha-test; not-a-command' \
    GHCR_TOKEN='allowed-secret' \
    GITHUB_TOKEN='must-not-be-forwarded' \
    bash "$RUNNER" "$test_root/step.sh"
)

grep -Fq "export RUNNER_TEMP=/tmp/deploy\\ path" <<<"$output"
grep -Fq "export IMAGE_TAG=sha-test\\;\\ not-a-command" <<<"$output"
grep -Fq 'export GHCR_TOKEN=allowed-secret' <<<"$output"
grep -Fq 'printf "remote script ran\n"' <<<"$output"
if grep -Fq 'must-not-be-forwarded' <<<"$output"; then
  echo "SSH step runner forwarded an environment variable outside its allowlist"
  exit 1
fi

echo "Aliyun SSH step runner ok"
