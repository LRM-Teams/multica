-- Receipts were workspace-scoped while director-generated idempotency keys
-- (e.g. "create_agent.cross_validator.v1") are only unique within one run:
-- a later run emitting the same key silently adopted the previous run's
-- agent, whose instructions still carried the previous run's mission.
-- Rescope receipt identity by session, matching the research_v6_outbox
-- uniqueness (workspace_id, session_id, idempotency_key).
-- Existing receipts cannot be attributed to a session and keeping them would
-- keep the cross-run reuse alive, so drop them.
TRUNCATE research_v6_runtime_effect;

ALTER TABLE research_v6_runtime_effect
  ADD COLUMN session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE;

ALTER TABLE research_v6_runtime_effect
  DROP CONSTRAINT research_v6_runtime_effect_pkey;

ALTER TABLE research_v6_runtime_effect
  ADD PRIMARY KEY (workspace_id, session_id, effect_kind, idempotency_key);
