#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
fixture_dir="$(mktemp -d)"
trap 'rm -rf "$fixture_dir"' EXIT

mkdir -p "$fixture_dir/scripts"
cp "$repo_root/scripts/check.sh" "$fixture_dir/scripts/check.sh"
printf '#!/usr/bin/env bash\nexit 23\n' > "$fixture_dir/scripts/ensure-postgres.sh"
printf '#!/usr/bin/env bash\n' > "$fixture_dir/scripts/local-env.sh"
printf 'POSTGRES_DB=test\n' > "$fixture_dir/.env"

set +e
output="$(
  cd "$fixture_dir"
  ENV_FILE=.env bash scripts/check.sh 2>&1
)"
status=$?
set -e

if [ "$status" -eq 0 ]; then
  echo "check.sh hid the PostgreSQL setup failure" >&2
  exit 1
fi
if ! grep -Fq "Checks FAILED." <<<"$output"; then
  echo "check.sh did not report the failed verification" >&2
  exit 1
fi
if grep -Fq "All checks passed." <<<"$output"; then
  echo "check.sh reported false success" >&2
  exit 1
fi

echo "check exit status propagation ok"
