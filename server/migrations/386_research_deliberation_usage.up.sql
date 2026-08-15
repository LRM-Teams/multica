ALTER TABLE research_deliberation
  ADD COLUMN policy_version TEXT NOT NULL DEFAULT 'research-deliberation-limits-v1',
  ADD COLUMN elapsed_seconds BIGINT NOT NULL DEFAULT 0 CHECK (elapsed_seconds >= 0),
  ADD COLUMN tokens_used BIGINT NOT NULL DEFAULT 0 CHECK (tokens_used >= 0),
  ADD COLUMN tool_calls_used INTEGER NOT NULL DEFAULT 0 CHECK (tool_calls_used >= 0);
