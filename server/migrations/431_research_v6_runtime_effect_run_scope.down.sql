-- Run-scoped receipts may share the same (workspace_id, effect_kind,
-- idempotency_key) across sessions, which the old primary key forbids.
-- Receipts are operational idempotency state, not user data: drop them
-- instead of collapsing rows arbitrarily.
TRUNCATE research_v6_runtime_effect;

ALTER TABLE research_v6_runtime_effect
  DROP CONSTRAINT research_v6_runtime_effect_pkey;

ALTER TABLE research_v6_runtime_effect
  DROP COLUMN session_id;

ALTER TABLE research_v6_runtime_effect
  ADD PRIMARY KEY (workspace_id, effect_kind, idempotency_key);
