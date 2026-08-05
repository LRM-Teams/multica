#!/usr/bin/env bash
# Download public skills (SKILL.md-only bundles) into canonical Agent roots for evolution E2E testing.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WS_ROOT="${MULTICA_WORKSPACES_ROOT:-$HOME/.multica/workspaces}"
WS_ID="f1b514db-54f1-48ec-b1a7-a5ac6d128977"
AGENTS_BASE="$WS_ROOT/$WS_ID/agents"

AGENT_CODE_REVIEW="78e90451-507e-4db9-86c5-a43e7d70d053"
AGENT_PI_CHAIN="dfb06fb1-d79e-4b23-b63c-451ff6ccad23"
AGENT_TEST="573468e1-04eb-4131-aedc-d51c471dc03b"
AGENT_TEST2="215b8de7-1f95-46d1-9603-d7c80099b072"

ensure_agent_root() {
  local root="$1"
  mkdir -p "$root"/{memory/daily,memory/audit,skills/drafts,skills/enabled,inbox/memory,inbox/skills,shared-cache/memory,shared-cache/skills,profile,feedback,sync_queue}
}

download_skill_md_only() {
  local url="$1" dest="$2"
  mkdir -p "$dest"
  curl -fsSL --max-time 30 "$url" -o "$dest/SKILL.md"
  python3 - "$dest" <<'PY'
import sys
from pathlib import Path
dest = Path(sys.argv[1])
text = (dest / "SKILL.md").read_text()
if not text.lstrip().startswith("---"):
    raise SystemExit(f"missing frontmatter in {dest}/SKILL.md")
print(f"  ok {(dest / 'SKILL.md').stat().st_size} bytes")
PY
}

write_skill_candidate() {
  local root="$1"
  local json_line="$2"
  : >"$root/sync_queue/skill-candidates.jsonl"
  printf '%s\n' "$json_line" >>"$root/sync_queue/skill-candidates.jsonl"
}

echo "==> Downloading public skills (SKILL.md only) into Agent skill drafts"

for agent_id in "$AGENT_CODE_REVIEW" "$AGENT_PI_CHAIN" "$AGENT_TEST" "$AGENT_TEST2"; do
  ensure_agent_root "$AGENTS_BASE/$agent_id"
done

echo "-- code_review: anthropics pdf (skills.sh/anthropics/skills/pdf)"
download_skill_md_only \
  "https://raw.githubusercontent.com/anthropics/skills/main/skills/pdf/SKILL.md" \
  "$AGENTS_BASE/$AGENT_CODE_REVIEW/skills/drafts/anthropics-pdf"
write_skill_candidate "$AGENTS_BASE/$AGENT_CODE_REVIEW" \
  '{"unit_type":"skill","local_unit_id":"web_anthropics_pdf","title":"PDF processing","summary":"Read, merge, split, and transform PDF files.","bundle_path":"../skills/drafts/anthropics-pdf","sensitivity":"none","confidence":"high","tags":["pdf","documents"],"task_types":["documentation","analysis"],"created_at":"2026-06-30T12:00:00Z"}'

echo "-- pi_sharing_chain_test: vercel composition-patterns"
download_skill_md_only \
  "https://raw.githubusercontent.com/vercel-labs/agent-skills/main/skills/composition-patterns/SKILL.md" \
  "$AGENTS_BASE/$AGENT_PI_CHAIN/skills/drafts/vercel-composition-patterns"
write_skill_candidate "$AGENTS_BASE/$AGENT_PI_CHAIN" \
  '{"unit_type":"skill","local_unit_id":"web_vercel_composition","title":"React composition patterns","summary":"Compound components and flexible React APIs without boolean prop proliferation.","bundle_path":"../skills/drafts/vercel-composition-patterns","sensitivity":"none","confidence":"high","tags":["react","typescript","ui"],"frameworks":["react"],"languages":["typescript"],"task_types":["implementation","refactor"],"created_at":"2026-06-30T12:00:00Z"}'

echo "-- test: obra brainstorming"
download_skill_md_only \
  "https://raw.githubusercontent.com/obra/superpowers/main/skills/brainstorming/SKILL.md" \
  "$AGENTS_BASE/$AGENT_TEST/skills/drafts/obra-brainstorming"
write_skill_candidate "$AGENTS_BASE/$AGENT_TEST" \
  '{"unit_type":"skill","local_unit_id":"web_obra_brainstorming","title":"Brainstorming before implementation","summary":"Explore requirements and design before writing code.","bundle_path":"../skills/drafts/obra-brainstorming","sensitivity":"none","confidence":"high","tags":["design","planning"],"task_types":["implementation","documentation"],"created_at":"2026-06-30T12:00:00Z"}'

echo "-- test2: anthropics pptx (skills.sh/anthropics/skills/pptx)"
download_skill_md_only \
  "https://raw.githubusercontent.com/anthropics/skills/main/skills/pptx/SKILL.md" \
  "$AGENTS_BASE/$AGENT_TEST2/skills/drafts/anthropics-pptx"
write_skill_candidate "$AGENTS_BASE/$AGENT_TEST2" \
  '{"unit_type":"skill","local_unit_id":"web_anthropics_pptx","title":"PPTX presentation skill","summary":"Create and edit PowerPoint presentations programmatically.","bundle_path":"../skills/drafts/anthropics-pptx","sensitivity":"none","confidence":"high","tags":["pptx","documents","presentation"],"task_types":["documentation"],"created_at":"2026-06-30T12:00:00Z"}'

echo "==> Resetting evolution/skill DB state for a clean run"
docker exec -i multica-postgres-1 psql -U multica -d multica -v ON_ERROR_STOP=1 <<'EOSQL'
BEGIN;

DELETE FROM agent_skill_suggestion;
DELETE FROM agent_skill;
DELETE FROM skill_file;
DELETE FROM skill;
DELETE FROM evolution_unit_submission_file;
DELETE FROM evolution_unit_submission;
UPDATE shared_evolution_unit SET current_version_id = NULL;
DELETE FROM shared_evolution_unit_file;
DELETE FROM shared_evolution_unit_version;
DELETE FROM shared_evolution_unit;

UPDATE agent SET
  description = 'Go and GitHub pull request code review agent',
  instructions = 'Review Go pull requests on GitHub. Check migrations, targeted tests, and CI before approving.'
WHERE id = '78e90451-507e-4db9-86c5-a43e7d70d053';

UPDATE agent SET
  description = 'Design-first agent for brainstorming and documentation workflows',
  instructions = 'Explore user intent and design before implementation. Strong at documentation and office document tasks.'
WHERE id = '573468e1-04eb-4131-aedc-d51c471dc03b';

UPDATE agent SET
  description = 'React and TypeScript UI implementation agent',
  instructions = 'Build React components with TypeScript. Prefer composition patterns, semantic tokens, and accessible UI.'
WHERE id = '215b8de7-1f95-46d1-9603-d7c80099b072';

UPDATE agent SET
  description = 'Evolution pipeline verification agent for Pi skill sharing',
  instructions = 'Verify Pi evolution upload, promotion, and cross-agent skill suggestion flows.'
WHERE id = 'dfb06fb1-d79e-4b23-b63c-451ff6ccad23';

COMMIT;
EOSQL

echo ""
echo "==> Waiting for daemon upload + curator (~70s)..."
sleep 70

echo "==> Materializing any promoted skills missing from catalog"
(cd "$ROOT/server" && go run ./cmd/materialize-promoted/)

echo "==> Rescanning agent skill suggestions"
(cd "$ROOT/server" && go run ./cmd/rescan-suggestions/)

echo ""
echo "==> Current state"
docker exec multica-postgres-1 psql -U multica -d multica -c "
SELECT status, count(*) FROM evolution_unit_submission WHERE workspace_id='$WS_ID' GROUP BY status ORDER BY status;
SELECT name FROM skill WHERE workspace_id='$WS_ID' AND source_evolution_unit_id IS NOT NULL ORDER BY name;
SELECT a.name AS agent, sk.name AS skill, s.action, round(s.matcher_score::numeric,2) AS score
FROM agent_skill_suggestion s
JOIN agent a ON a.id = s.agent_id
JOIN skill sk ON sk.id = s.skill_id
WHERE s.workspace_id='$WS_ID' AND s.status='pending'
ORDER BY 1,2;
"

echo ""
echo "Open web UI → workspace LRM-jian → each agent's Skills tab to accept suggestions."
