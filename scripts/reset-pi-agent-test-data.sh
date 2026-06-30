#!/usr/bin/env bash
# Reset local Pi agent dirs + DB memory/evolution data, then seed test skills/memory.
set -euo pipefail

WS_ROOT="${MULTICA_WORKSPACES_ROOT:-$HOME/multica_workspaces}"
WS_ID="f1b514db-54f1-48ec-b1a7-a5ac6d128977"
USER_ID="160e59a2-e057-4770-994a-a26a7ffca8f9"
AGENTS_BASE="$WS_ROOT/$WS_ID/.pi/agents"

declare -A AGENT_NAMES=(
  ["78e90451-507e-4db9-86c5-a43e7d70d053"]="code_review"
  ["dfb06fb1-d79e-4b23-b63c-451ff6ccad23"]="pi_sharing_chain_test"
  ["573468e1-04eb-4131-aedc-d51c471dc03b"]="test"
  ["215b8de7-1f95-46d1-9603-d7c80099b072"]="test2"
)

ensure_agent_root() {
  local root="$1"
  local dirs=(
    "$root/memory/daily"
    "$root/memory/audit"
    "$root/skills/drafts"
    "$root/skills/generated"
    "$root/skills/enabled"
    "$root/inbox/memory"
    "$root/inbox/skills"
    "$root/shared-cache/memory"
    "$root/shared-cache/skills"
    "$root/profile"
    "$root/feedback"
    "$root/sync_queue"
  )
  for dir in "${dirs[@]}"; do
    mkdir -p "$dir"
  done
}

echo "==> Cleaning local Pi agent directories under $AGENTS_BASE"
rm -rf "$AGENTS_BASE"
mkdir -p "$AGENTS_BASE"

write_skill_draft() {
  local root="$1"
  local skill_id="$2"
  local name="$3"
  local description="$4"
  local body="$5"
  local draft_dir="$root/skills/drafts/$skill_id"
  mkdir -p "$draft_dir"
  cat >"$draft_dir/SKILL.md" <<EOF
---
name: $name
description: $description
---

$body
EOF
}

append_jsonl() {
  local file="$1"
  local line="$2"
  printf '%s\n' "$line" >>"$file"
}

echo "==> Seeding local agent files"
for agent_id in "${!AGENT_NAMES[@]}"; do
  agent_name="${AGENT_NAMES[$agent_id]}"
  root="$AGENTS_BASE/$agent_id"
  ensure_agent_root "$root"

  cat >"$root/memory/daily/2026-06-30.md" <<EOF
# Daily notes — $agent_name

- Prefer small, reviewable diffs.
- Run targeted tests before broad suites.
- Keep evolution candidates in sync_queue until promoted.
EOF

  case "$agent_name" in
    code_review)
      append_jsonl "$root/sync_queue/memory-candidates.jsonl" '{"unit_type":"memory","local_unit_id":"mem_code_review_git_001","title":"Go PR review checklist","summary":"Verify migrations and targeted tests before approving Go pull requests.","content":"When reviewing Go PRs, check database migrations, require package-scoped tests, and confirm CI is green.","sensitivity":"none","confidence":"high","suggested_scope":"workspace","tags":["go","review"],"tools":["github"],"languages":["go"],"task_types":["review"],"created_at":"2026-06-30T00:00:00Z"}'
      write_skill_draft "$root" "go-pr-review-local" "go-pr-review-local" "Local Go PR review checklist for evolution testing" "# Go PR Review\n\n- Check migrations\n- Require targeted tests\n- Confirm CI"
      append_jsonl "$root/sync_queue/skill-candidates.jsonl" '{"unit_type":"skill","local_unit_id":"skill_go_pr_review_local","title":"Go PR review helper","summary":"Review Go pull requests with migration and test checks.","bundle_path":"../skills/drafts/go-pr-review-local","sensitivity":"none","confidence":"high","tags":["go","review","github"],"tools":["github"],"languages":["go"],"task_types":["review"],"created_at":"2026-06-30T00:00:00Z"}'
      ;;
    pi_sharing_chain_test)
      append_jsonl "$root/sync_queue/memory-candidates.jsonl" '{"unit_type":"memory","local_unit_id":"mem_git_safety_001","title":"Prefer non-destructive git operations","summary":"Avoid resetting user changes unless explicitly requested.","content":"When working in a dirty tree, do not revert changes that the agent did not create.","sensitivity":"none","confidence":"high","suggested_scope":"workspace","tags":["git","safety"],"task_types":["code-editing"],"created_at":"2026-06-30T00:00:00Z"}'
      write_skill_draft "$root" "git-safe-ops" "git-safe-ops" "Safe git workflow for shared evolution testing" "# Git Safe Ops\n\n- Never force-push without explicit approval\n- Prefer stash over reset"
      append_jsonl "$root/sync_queue/skill-candidates.jsonl" '{"unit_type":"skill","local_unit_id":"skill_git_safe_ops","title":"Git safe operations","summary":"Non-destructive git workflow for team repos.","bundle_path":"../skills/drafts/git-safe-ops","sensitivity":"none","confidence":"high","tags":["git","safety"],"task_types":["code-editing"],"created_at":"2026-06-30T00:00:00Z"}'
      ;;
    test)
      append_jsonl "$root/sync_queue/memory-candidates.jsonl" '{"unit_type":"memory","local_unit_id":"mem_test_docs_001","title":"Documentation tone guide","summary":"Write concise product docs with runnable examples.","content":"Lead with user goals, keep sections short, and link instead of duplicating content.","sensitivity":"none","confidence":"high","tags":["docs","writing"],"task_types":["documentation"],"created_at":"2026-06-30T00:00:00Z"}'
      ;;
    test2)
      append_jsonl "$root/sync_queue/memory-candidates.jsonl" '{"unit_type":"memory","local_unit_id":"mem_docker_build_001","title":"Docker build conventions","summary":"Use multi-stage builds and pin base image digests.","content":"Keep runtime images small, scan for CVEs, and tag with git SHA plus semver.","sensitivity":"none","confidence":"high","tags":["docker","devops"],"tools":["docker"],"task_types":["build"],"created_at":"2026-06-30T00:00:00Z"}'
      write_skill_draft "$root" "docker-build-local" "docker-build-local" "Local Docker build checklist for evolution testing" "# Docker Build\n\n- Multi-stage builds\n- Pin digests\n- Scan before publish"
      append_jsonl "$root/sync_queue/skill-candidates.jsonl" '{"unit_type":"skill","local_unit_id":"skill_docker_build_local","title":"Docker build helper","summary":"Build and publish container images safely.","bundle_path":"../skills/drafts/docker-build-local","sensitivity":"none","confidence":"high","tags":["docker","devops"],"tools":["docker"],"task_types":["build"],"created_at":"2026-06-30T00:00:00Z"}'
      ;;
  esac

  echo "  - $agent_name ($agent_id)"
done

echo "==> Cleaning DB memory/evolution data and re-seeding agent_memory"
docker exec -i multica-postgres-1 psql -U multica -d multica -v ON_ERROR_STOP=1 <<'EOSQL'
BEGIN;

DELETE FROM agent_skill_suggestion;
DELETE FROM agent_skill;
DELETE FROM agent_memory;
DELETE FROM evolution_unit_submission_file;
DELETE FROM evolution_unit_submission;
UPDATE shared_evolution_unit SET current_version_id = NULL;
DELETE FROM shared_evolution_unit_file;
DELETE FROM shared_evolution_unit_version;
DELETE FROM shared_evolution_unit;

DELETE FROM skill_file;
DELETE FROM skill;

WITH ws AS (
  SELECT 'f1b514db-54f1-48ec-b1a7-a5ac6d128977'::uuid AS id,
         '160e59a2-e057-4770-994a-a26a7ffca8f9'::uuid AS user_id
),
inserted AS (
  INSERT INTO skill (workspace_id, name, description, content, config, created_by)
  SELECT ws.id, v.name, v.description, v.content, v.config::jsonb, ws.user_id
  FROM ws,
  (VALUES
    ('go-github-pr-review', 'Review Go pull requests on GitHub.', '# Go GitHub PR Review\n\nVerify migrations and targeted tests.', '{"tags":["go","review","github"],"tools":["github"],"languages":["go"],"task_types":["review"]}'),
    ('docker-image-builder', 'Build Docker images for services.', '# Docker Image Builder\n\nUse multi-stage builds and scan images.', '{"tags":["docker","devops"],"tools":["docker"],"task_types":["build"]}'),
    ('docs-writer', 'Write product documentation.', '# Documentation Writer\n\nLead with user goals and runnable examples.', '{"tags":["docs","writing"],"task_types":["documentation"]}'),
    ('python-data-analysis', 'Analyze datasets with pandas.', '# Python Data Analysis\n\nValidate schema before aggregations.', '{"tags":["python","data"],"languages":["python"],"task_types":["analysis"]}'),
    ('react-ui-components', 'Build React UI with TypeScript.', '# React UI Components\n\nUse semantic tokens and accessible patterns.', '{"tags":["react","typescript","ui"],"languages":["typescript"],"frameworks":["react"],"task_types":["implementation"]}')
  ) AS v(name, description, content, config)
  RETURNING id
)
INSERT INTO skill_file (skill_id, path, content)
SELECT s.id, 'SKILL.md', s.content FROM inserted i JOIN skill s ON s.id = i.id;

INSERT INTO agent_memory (workspace_id, agent_id, name, content, config, sync_key, content_hash, created_by)
VALUES
  ('f1b514db-54f1-48ec-b1a7-a5ac6d128977', '78e90451-507e-4db9-86c5-a43e7d70d053', 'Go PR review notes', 'Check migrations, targeted tests, and CI before approving Go pull requests.', '{}', 'memory/daily/go-pr-review', 'hash-go-pr-review', '160e59a2-e057-4770-994a-a26a7ffca8f9'),
  ('f1b514db-54f1-48ec-b1a7-a5ac6d128977', 'dfb06fb1-d79e-4b23-b63c-451ff6ccad23', 'Git safety notes', 'Do not revert or reset changes the agent did not create unless the user explicitly asks.', '{}', 'memory/daily/git-safety', 'hash-git-safety', '160e59a2-e057-4770-994a-a26a7ffca8f9'),
  ('f1b514db-54f1-48ec-b1a7-a5ac6d128977', '573468e1-04eb-4131-aedc-d51c471dc03b', 'Docs tone guide', 'Write concise docs with runnable examples and scannable headings.', '{}', 'memory/daily/docs-tone', 'hash-docs-tone', '160e59a2-e057-4770-994a-a26a7ffca8f9'),
  ('f1b514db-54f1-48ec-b1a7-a5ac6d128977', '215b8de7-1f95-46d1-9603-d7c80099b072', 'Docker build notes', 'Use multi-stage builds, pin digests, and scan images before publish.', '{}', 'memory/daily/docker-build', 'hash-docker-build', '160e59a2-e057-4770-994a-a26a7ffca8f9');

COMMIT;

SELECT 'skill' AS t, count(*) FROM skill
UNION ALL SELECT 'agent_memory', count(*) FROM agent_memory
UNION ALL SELECT 'evolution_unit_submission', count(*) FROM evolution_unit_submission;
EOSQL

echo ""
echo "Done. Local roots: $AGENTS_BASE/{agent_id}/"
echo "Evolution candidates will sync on next daemon shared-skills loop (or restart daemon)."
