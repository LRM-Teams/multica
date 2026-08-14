ALTER TABLE research_deliberation
  DROP COLUMN IF EXISTS tool_calls_used,
  DROP COLUMN IF EXISTS tokens_used,
  DROP COLUMN IF EXISTS elapsed_seconds,
  DROP COLUMN IF EXISTS policy_version;
