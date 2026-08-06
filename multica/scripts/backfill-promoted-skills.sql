BEGIN;

INSERT INTO skill (workspace_id, name, description, content, config, created_by, source_evolution_unit_id)
SELECT
  s.workspace_id,
  trim(substring(f.content from 'name: ([^\n\r]+)')),
  coalesce(nullif(trim(substring(f.content from 'description: ([^\n\r]+)')), ''), s.summary),
  f.content,
  jsonb_build_object('origin', 'evolution', 'evolution_unit_id', s.promoted_unit_id::text),
  m.user_id,
  s.promoted_unit_id
FROM evolution_unit_submission s
JOIN evolution_unit_submission_file f ON f.submission_id = s.id AND f.path = 'SKILL.md'
JOIN member m ON m.id = s.source_member_id
WHERE s.workspace_id = 'f1b514db-54f1-48ec-b1a7-a5ac6d128977'
  AND s.status = 'promoted'
  AND s.unit_type = 'skill'
  AND s.promoted_unit_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM skill sk
    WHERE sk.workspace_id = s.workspace_id
      AND sk.source_evolution_unit_id = s.promoted_unit_id
  );

INSERT INTO skill_file (skill_id, path, content)
SELECT sk.id, 'SKILL.md', sk.content
FROM skill sk
WHERE sk.workspace_id = 'f1b514db-54f1-48ec-b1a7-a5ac6d128977'
  AND sk.source_evolution_unit_id IS NOT NULL
ON CONFLICT (skill_id, path) DO UPDATE SET content = EXCLUDED.content;

COMMIT;

SELECT sk.name, u.title AS unit_title
FROM skill sk
JOIN shared_evolution_unit u ON u.id = sk.source_evolution_unit_id
WHERE sk.workspace_id = 'f1b514db-54f1-48ec-b1a7-a5ac6d128977'
ORDER BY sk.name;
