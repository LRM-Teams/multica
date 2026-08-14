ALTER TABLE research_director_cycle DROP CONSTRAINT IF EXISTS research_v6_cycle_attempt_fk;
ALTER TABLE research_director_cycle DROP CONSTRAINT IF EXISTS research_v6_cycle_work_item_fk;
ALTER TABLE research_task
  DROP COLUMN IF EXISTS work_item_id, DROP COLUMN IF EXISTS expected_result_schema_id,
  DROP COLUMN IF EXISTS required_capabilities, DROP COLUMN IF EXISTS task_payload,
  DROP COLUMN IF EXISTS task_schema_id, DROP COLUMN IF EXISTS task_type;
ALTER TABLE research_task DROP CONSTRAINT IF EXISTS research_task_kind_check;
ALTER TABLE research_task ADD CONSTRAINT research_task_kind_check CHECK (kind IN (
  'plan','discover','deep_read','verify','counter_search','replan','synthesize','quality_gate','citation_audit'
));
DROP TABLE IF EXISTS research_work_catalog_page;
DROP TABLE IF EXISTS research_work_item_attempt;
DROP TRIGGER IF EXISTS research_v6_work_item_assignee_sync ON research_work_item;
DROP FUNCTION IF EXISTS research_v6_work_item_assignee_sync_fn();
DROP INDEX IF EXISTS research_v6_work_item_claim_idx;
DROP INDEX IF EXISTS research_v6_work_item_idempotency_idx;
ALTER TABLE research_work_item DROP CONSTRAINT IF EXISTS research_v6_work_item_cycle_fk;
ALTER TABLE research_work_item DROP CONSTRAINT IF EXISTS research_v6_work_item_values_check;
ALTER TABLE research_work_item
  DROP COLUMN IF EXISTS cancelled_at, DROP COLUMN IF EXISTS started_at, DROP COLUMN IF EXISTS ready_at,
  DROP COLUMN IF EXISTS terminal_reason_detail, DROP COLUMN IF EXISTS terminal_reason_code,
  DROP COLUMN IF EXISTS payload_schema_id, DROP COLUMN IF EXISTS lease_expires_at, DROP COLUMN IF EXISTS lease_token,
  DROP COLUMN IF EXISTS attempt_count, DROP COLUMN IF EXISTS max_attempts, DROP COLUMN IF EXISTS priority,
  DROP COLUMN IF EXISTS assigned_agent_id, DROP COLUMN IF EXISTS created_by_director_cycle_id,
  DROP COLUMN IF EXISTS input_event_sequence, DROP COLUMN IF EXISTS input_state_version,
  DROP COLUMN IF EXISTS goal_version, DROP COLUMN IF EXISTS idempotency_key, DROP COLUMN IF EXISTS client_key,
  DROP COLUMN IF EXISTS target_id, DROP COLUMN IF EXISTS target_kind;
ALTER TABLE research_work_item DROP CONSTRAINT IF EXISTS research_work_item_status_check;
ALTER TABLE research_work_item ADD CONSTRAINT research_work_item_status_check
  CHECK (status IN ('pending','enqueued','done','cancelled','failed'));
ALTER TABLE research_work_item DROP CONSTRAINT IF EXISTS research_work_item_kind_check;
ALTER TABLE research_work_item ADD CONSTRAINT research_work_item_kind_check
  CHECK (kind IN ('expand_subquestion','evidence_gap','resolve_conflict','advance_probe'));
