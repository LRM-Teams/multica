#!/usr/bin/env bash
set -euo pipefail

phase=${1:?usage: assert-runner-workspace-ownership.sh <phase>}
expected_user=${RUNNER_EXPECTED_USER:?RUNNER_EXPECTED_USER is required}
expected_owner=${WORKSPACE_EXPECTED_OWNER:-$expected_user}
actual_user=$(id -un)

if [[ "$actual_user" != "$expected_user" ]]; then
  echo "::error title=Unexpected runner identity::phase=${phase} expected=${expected_user} actual=${actual_user}"
  exit 1
fi

if [[ -n ${RUNNER_WORK_ROOT:-} ]]; then
  runner_work_root=$RUNNER_WORK_ROOT
elif [[ -n ${RUNNER_WORKSPACE:-} ]]; then
  runner_work_root=$(dirname -- "$RUNNER_WORKSPACE")
else
  echo "::error title=Runner work root unavailable::phase=${phase} RUNNER_WORKSPACE is empty"
  exit 1
fi

if [[ ! -d "$runner_work_root" ]]; then
  echo "::error title=Runner work root unavailable::phase=${phase} path=${runner_work_root}"
  exit 1
fi

report_dir=${RUNNER_TEMP:-${TMPDIR:-/tmp}}
ownership_report=$(mktemp "${report_dir%/}/runner-ownership.XXXXXX")
find_errors=$(mktemp "${report_dir%/}/runner-ownership-errors.XXXXXX")
trap 'rm -f "$ownership_report" "$find_errors"' EXIT

# A host-side invocation may inherit a cwd that the runner cannot traverse.
# The work-root path is absolute, so scan it from a neutral directory.
cd /

find_status=0
if find "$runner_work_root" -xdev ! -user "$expected_owner" -print0 >"$ownership_report" 2>"$find_errors"; then
  :
else
  find_status=$?
fi

offender_count=0
while IFS= read -r -d '' path; do
  if metadata=$(stat -c '%U:%G mode=%a' -- "$path" 2>/dev/null); then
    :
  else
    metadata=$(stat -f '%Su:%Sg mode=%Lp' -- "$path")
  fi
  echo "::error title=Runner workspace ownership violation::phase=${phase} owner=${metadata} path=${path}"
  offender_count=$((offender_count + 1))
done <"$ownership_report"

if (( find_status != 0 )); then
  echo "::error title=Runner ownership scan failed::phase=${phase} root=${runner_work_root}"
  sed 's/^/  /' "$find_errors"
  exit 1
fi

if (( offender_count > 0 )); then
  echo "::error title=Runner workspace ownership violation::phase=${phase} root=${runner_work_root} expected_owner=${expected_owner} offenders=${offender_count}"
  exit 1
fi

echo "Runner workspace ownership OK: phase=${phase} root=${runner_work_root} owner=${expected_owner}"
