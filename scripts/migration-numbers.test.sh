#!/usr/bin/env bash
# Rejects a NEWLY ADDED migration that reuses a version number already present
# on the base branch.
#
# Incremental only, deliberately. `dev` already carries 59 duplicated numbers
# (020 and 026 through 247), so a whole-directory check would fail on its first
# run forever. Worse, the only way to make such a check pass would be renaming
# applied migrations — and the migrator records `version` as the full filename
# (`ExtractVersion` returns the basename), so a rename makes production believe
# the migration never ran. Existing duplicates must stay exactly as they are.
#
# What the number is for: same-numbered migrations execute in lexicographic
# filename order, so the number stops expressing order once it is shared. That
# is tolerable while no two same-numbered migrations depend on each other, and
# unrecoverable if they ever do.
set -euo pipefail

MIGRATIONS_DIR="${MIGRATIONS_DIR:-server/migrations}"
BASE_REF="${BASE_REF:-origin/dev}"

# Every version number that exists on the base branch.
base_numbers="$(
  git ls-tree --name-only "$BASE_REF" "$MIGRATIONS_DIR/" 2>/dev/null |
    sed 's|.*/||' | grep -oE '^[0-9]+' | sort -u
)"

# Files added by this branch (A = added only; edits to existing ones are fine).
added_files="$(
  git diff --diff-filter=A --name-only "$BASE_REF"...HEAD -- "$MIGRATIONS_DIR/" 2>/dev/null || true
)"

if [[ -z "$added_files" ]]; then
  echo "migration version numbers ok (no new migrations)"
  exit 0
fi

status=0
declare -A added_numbers=()
while IFS= read -r path; do
  [[ -z "$path" ]] && continue
  file="${path##*/}"
  number="$(grep -oE '^[0-9]+' <<<"$file" || true)"
  migration="${file%.up.sql}"
  migration="${migration%.down.sql}"

  if [[ -z "$number" ]]; then
    echo "Migration filename must start with a version number: $file"
    status=1
    continue
  fi

  if grep -qxF "$number" <<<"$base_numbers"; then
    echo "New migration reuses version number $number already on $BASE_REF: $file"
    echo "  Existing:"
    git ls-tree --name-only "$BASE_REF" "$MIGRATIONS_DIR/" |
      sed 's|.*/||' | grep -E "^${number}_" | sed 's/^/    /' | sort -u
    echo "  Pick the next free number. Do NOT rename the existing ones —"
    echo "  version is the filename, so renaming makes production treat them as unrun."
    echo "  Prefer: multica migration reserve --number <N> --filename <file>"
    status=1
  fi

  # Also reject two newly added migrations on this branch that share a number
  # (the #2567 vs #2568 class of collision before either lands on base).
  if [[ -n "${added_numbers[$number]:-}" && "${added_numbers[$number]}" != "$migration" ]]; then
    echo "New migrations on this branch collide on version number $number:"
    echo "    ${added_numbers[$number]}"
    echo "    $migration"
    echo "  Reserve distinct numbers with: multica migration reserve"
    status=1
  else
    added_numbers["$number"]="$migration"
  fi
done <<<"$added_files"

if [[ "$status" -eq 0 ]]; then
  echo "migration version numbers ok"
fi
exit "$status"
