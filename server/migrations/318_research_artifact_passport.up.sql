-- Chapter D1: Research artifact passport foundation.
--
-- Normalized passport/version/policy/context/lineage tables, scoped composite
-- uniqueness for tenant/session isolation, fail-closed kind registry, and
-- honest legacy backfill metadata. Agent authorization behavior is unchanged
-- until D2 wires manifest assembly.

-- ---------------------------------------------------------------------------
-- 1) Fail-closed registry helpers
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION research_artifact_entity_kind_allowed(kind TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT kind IN (
    'run_session', 'contract_revision', 'method_decision', 'question', 'task',
    'attempt', 'result_artifact', 'legacy_source', 'source_snapshot',
    'observation', 'claim', 'evidence_link', 'report_revision',
    'evaluation_decision', 'stage_evaluation', 'research_message',
    'product_round_decision', 'context_manifest', 'run_event', 'graph_node',
    'graph_edge'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_lifecycle_status_allowed(status TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT status IN (
    'registered', 'accepted', 'rejected', 'stale', 'superseded', 'withdrawn'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_provenance_completeness_allowed(value TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT value IN ('complete', 'partial', 'unknown');
$$;

CREATE OR REPLACE FUNCTION research_artifact_access_level_allowed(level TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT level IN ('verified_only', 'redacted', 'raw');
$$;

CREATE OR REPLACE FUNCTION research_artifact_hash_origin_allowed(origin TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT origin IN ('production', 'migration_recomputed', 'legacy_stored');
$$;

CREATE OR REPLACE FUNCTION research_artifact_mutation_kind_allowed(kind TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT kind IN (
    'artifact_create', 'current_version', 'access', 'lifecycle', 'verification',
    'supersession', 'eligibility', 'grant_create', 'grant_revoke'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_context_representation_allowed(value TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT value IN ('metadata', 'excerpt', 'full');
$$;

CREATE OR REPLACE FUNCTION research_artifact_context_omission_reason_allowed(reason TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT reason IN (
    'access_denied', 'stale', 'superseded', 'duplicate', 'token_budget', 'irrelevant'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_content_hash_valid(hash TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT hash ~ '^sha256:[0-9a-f]{64}$';
$$;

-- ---------------------------------------------------------------------------
-- 2) Scoped composite uniqueness for session-owned Research tables
-- ---------------------------------------------------------------------------

CREATE UNIQUE INDEX IF NOT EXISTS research_session_workspace_id_uidx
  ON research_session (workspace_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_contract_revision_scoped_uidx
  ON research_contract_revision (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_question_scoped_uidx
  ON research_question (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_task_scoped_uidx
  ON research_task (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_task_attempt_scoped_uidx
  ON research_task_attempt (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_source_snapshot_scoped_uidx
  ON research_source_snapshot (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_observation_scoped_uidx
  ON research_observation (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_claim_scoped_uidx
  ON research_claim (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_report_scoped_uidx
  ON research_report (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_decision_scoped_uidx
  ON research_decision (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_stage_eval_scoped_uidx
  ON research_stage_eval (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_message_scoped_uidx
  ON research_message (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_product_round_card_scoped_uidx
  ON research_product_round_card (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_source_scoped_uidx
  ON research_source (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_graph_node_scoped_uidx
  ON research_graph_node (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_graph_edge_scoped_uidx
  ON research_graph_edge (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_claim_evidence_scoped_uidx
  ON research_claim_evidence (workspace_id, session_id, id);

CREATE UNIQUE INDEX IF NOT EXISTS research_run_event_scoped_uidx
  ON research_run_event (workspace_id, session_id, id);

-- ---------------------------------------------------------------------------
-- 3) Passport core tables
-- ---------------------------------------------------------------------------

CREATE TABLE research_artifact_passport (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  entity_kind TEXT NOT NULL,
  current_version INTEGER CHECK (current_version IS NULL OR current_version >= 1),
  eligibility_revision BIGINT NOT NULL DEFAULT 1 CHECK (eligibility_revision >= 1),
  lifecycle_status TEXT NOT NULL DEFAULT 'registered',
  provenance_completeness TEXT NOT NULL DEFAULT 'unknown',
  source_created_at TIMESTAMPTZ,
  registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  accepted_at TIMESTAMPTZ,
  superseded_at TIMESTAMPTZ,
  CONSTRAINT research_artifact_passport_entity_kind_check
    CHECK (research_artifact_entity_kind_allowed(entity_kind)),
  CONSTRAINT research_artifact_passport_lifecycle_status_check
    CHECK (research_artifact_lifecycle_status_allowed(lifecycle_status)),
  CONSTRAINT research_artifact_passport_provenance_completeness_check
    CHECK (research_artifact_provenance_completeness_allowed(provenance_completeness)),
  CONSTRAINT research_artifact_passport_session_fkey
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session (workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_passport_scoped_uidx
    UNIQUE (workspace_id, session_id, id),
  CONSTRAINT research_artifact_passport_kind_scoped_uidx
    UNIQUE (workspace_id, session_id, entity_kind, id)
);

CREATE INDEX research_artifact_passport_session_kind_idx
  ON research_artifact_passport (workspace_id, session_id, entity_kind);

CREATE TABLE research_artifact_version (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  artifact_id UUID NOT NULL,
  version INTEGER NOT NULL CHECK (version >= 1),
  schema_name TEXT NOT NULL DEFAULT '',
  schema_version TEXT NOT NULL DEFAULT '',
  canonicalization_version TEXT NOT NULL DEFAULT 'research-artifact-c14n-v1',
  content_hash TEXT NOT NULL,
  access_level TEXT NOT NULL,
  goal_version INTEGER CHECK (goal_version IS NULL OR goal_version >= 1),
  plan_version INTEGER CHECK (plan_version IS NULL OR plan_version >= 1),
  contract_revision_id UUID,
  strategy_version_id UUID CHECK (strategy_version_id IS NULL),
  produced_by_task_id UUID,
  produced_by_attempt_id UUID,
  produced_by_agent_id UUID,
  model TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '',
  execution_adapter TEXT NOT NULL DEFAULT '',
  hash_origin TEXT NOT NULL DEFAULT 'production',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT research_artifact_version_access_level_check
    CHECK (research_artifact_access_level_allowed(access_level)),
  CONSTRAINT research_artifact_version_hash_origin_check
    CHECK (research_artifact_hash_origin_allowed(hash_origin)),
  CONSTRAINT research_artifact_version_content_hash_check
    CHECK (research_artifact_content_hash_valid(content_hash)),
  CONSTRAINT research_artifact_version_passport_fkey
    FOREIGN KEY (workspace_id, session_id, artifact_id)
    REFERENCES research_artifact_passport (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_version_scoped_uidx
    UNIQUE (workspace_id, session_id, id),
  CONSTRAINT research_artifact_version_per_passport_uidx
    UNIQUE (workspace_id, session_id, artifact_id, version),
  CONSTRAINT research_artifact_version_contract_fkey
    FOREIGN KEY (workspace_id, session_id, contract_revision_id)
    REFERENCES research_contract_revision (workspace_id, session_id, id),
  CONSTRAINT research_artifact_version_task_fkey
    FOREIGN KEY (workspace_id, session_id, produced_by_task_id)
    REFERENCES research_task (workspace_id, session_id, id),
  CONSTRAINT research_artifact_version_attempt_fkey
    FOREIGN KEY (workspace_id, session_id, produced_by_attempt_id)
    REFERENCES research_task_attempt (workspace_id, session_id, id)
);

CREATE INDEX research_artifact_version_artifact_idx
  ON research_artifact_version (workspace_id, session_id, artifact_id, version DESC);

ALTER TABLE research_artifact_passport
  ADD CONSTRAINT research_artifact_passport_current_version_fkey
  FOREIGN KEY (workspace_id, session_id, id, current_version)
  REFERENCES research_artifact_version (workspace_id, session_id, artifact_id, version)
  DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE research_artifact_policy_state (
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  policy_version TEXT NOT NULL DEFAULT 'legacy-v1-v5-compat-v1',
  watermark BIGINT NOT NULL DEFAULT 0 CHECK (watermark >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, session_id),
  CONSTRAINT research_artifact_policy_state_session_fkey
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session (workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_policy_state_watermark_uidx
    UNIQUE (workspace_id, session_id, watermark)
);

CREATE TABLE research_artifact_policy_grant (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  principal_kind TEXT NOT NULL CHECK (principal_kind IN ('user', 'agent', 'system')),
  principal_id UUID NOT NULL,
  purpose TEXT NOT NULL CHECK (btrim(purpose) <> ''),
  normal_clearance TEXT,
  evaluation_private BOOLEAN NOT NULL DEFAULT false,
  policy_version TEXT NOT NULL DEFAULT 'legacy-v1-v5-compat-v1',
  revision BIGINT NOT NULL DEFAULT 1 CHECK (revision >= 1),
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ,
  CONSTRAINT research_artifact_policy_grant_compartment_check CHECK (
    (evaluation_private = true AND normal_clearance IS NULL)
    OR (evaluation_private = false AND normal_clearance IS NOT NULL
        AND research_artifact_access_level_allowed(normal_clearance))
  ),
  CONSTRAINT research_artifact_policy_grant_session_fkey
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session (workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_policy_grant_scoped_uidx
    UNIQUE (workspace_id, session_id, id)
);

CREATE TABLE research_artifact_policy_mutation (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  watermark BIGINT NOT NULL CHECK (watermark >= 0),
  mutation_kind TEXT NOT NULL,
  artifact_id UUID,
  policy_grant_id UUID,
  old_eligibility_revision BIGINT,
  new_eligibility_revision BIGINT,
  old_current_version INTEGER,
  new_current_version INTEGER,
  old_access_level TEXT,
  new_access_level TEXT,
  old_lifecycle_status TEXT,
  new_lifecycle_status TEXT,
  old_grant_revision BIGINT,
  new_grant_revision BIGINT,
  old_grant_status TEXT,
  new_grant_status TEXT,
  eligibility_reason TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT research_artifact_policy_mutation_kind_check
    CHECK (research_artifact_mutation_kind_allowed(mutation_kind)),
  CONSTRAINT research_artifact_policy_mutation_session_fkey
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session (workspace_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_policy_mutation_target_check CHECK (
    (artifact_id IS NOT NULL AND policy_grant_id IS NULL
     AND old_eligibility_revision IS NOT NULL AND new_eligibility_revision IS NOT NULL
     AND new_eligibility_revision = old_eligibility_revision + 1)
    OR
    (policy_grant_id IS NOT NULL AND artifact_id IS NULL
     AND old_grant_revision IS NOT NULL AND new_grant_revision IS NOT NULL
     AND new_grant_revision = old_grant_revision + 1)
  )
);

CREATE UNIQUE INDEX research_artifact_policy_mutation_artifact_revision_uidx
  ON research_artifact_policy_mutation (workspace_id, session_id, artifact_id, new_eligibility_revision)
  WHERE artifact_id IS NOT NULL;

CREATE UNIQUE INDEX research_artifact_policy_mutation_grant_revision_uidx
  ON research_artifact_policy_mutation (workspace_id, session_id, policy_grant_id, new_grant_revision)
  WHERE policy_grant_id IS NOT NULL;

CREATE TABLE research_result_artifact (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  attempt_id UUID NOT NULL,
  orchestrator_version TEXT NOT NULL DEFAULT '',
  result_schema_version TEXT NOT NULL DEFAULT '',
  result JSONB NOT NULL DEFAULT '{}'::jsonb,
  client_request_id TEXT NOT NULL DEFAULT '',
  content_hash TEXT NOT NULL,
  acceptance_policy_watermark BIGINT,
  accepted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT research_result_artifact_content_hash_check
    CHECK (research_artifact_content_hash_valid(content_hash)),
  CONSTRAINT research_result_artifact_passport_fkey
    FOREIGN KEY (workspace_id, session_id, id)
    REFERENCES research_artifact_passport (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_result_artifact_attempt_fkey
    FOREIGN KEY (workspace_id, session_id, attempt_id)
    REFERENCES research_task_attempt (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_result_artifact_scoped_uidx
    UNIQUE (workspace_id, session_id, id),
  CONSTRAINT research_result_artifact_attempt_uidx
    UNIQUE (workspace_id, session_id, attempt_id)
);

CREATE TABLE research_artifact_context_manifest (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  attempt_id UUID NOT NULL,
  task_id UUID NOT NULL,
  purpose TEXT NOT NULL DEFAULT '',
  policy_version TEXT NOT NULL DEFAULT 'legacy-v1-v5-compat-v1',
  policy_watermark BIGINT NOT NULL DEFAULT 0 CHECK (policy_watermark >= 0),
  through_state_version BIGINT NOT NULL DEFAULT 0 CHECK (through_state_version >= 0),
  normal_grant_id UUID,
  normal_grant_revision BIGINT,
  evaluation_grant_id UUID,
  evaluation_grant_revision BIGINT,
  principal_header_bytes BYTEA NOT NULL DEFAULT ''::bytea,
  principal_header_hash TEXT NOT NULL DEFAULT '',
  manifest_hash TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT research_artifact_context_manifest_passport_fkey
    FOREIGN KEY (workspace_id, session_id, id)
    REFERENCES research_artifact_passport (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_context_manifest_attempt_fkey
    FOREIGN KEY (workspace_id, session_id, attempt_id)
    REFERENCES research_task_attempt (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_context_manifest_task_fkey
    FOREIGN KEY (workspace_id, session_id, task_id)
    REFERENCES research_task (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_context_manifest_scoped_uidx
    UNIQUE (workspace_id, session_id, id),
  CONSTRAINT research_artifact_context_manifest_attempt_uidx
    UNIQUE (workspace_id, session_id, attempt_id)
);

CREATE TABLE research_artifact_context_entry (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  manifest_id UUID NOT NULL,
  ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
  artifact_version_id UUID NOT NULL,
  eligibility_revision BIGINT NOT NULL CHECK (eligibility_revision >= 1),
  representation TEXT NOT NULL,
  representation_bytes BYTEA NOT NULL DEFAULT ''::bytea,
  representation_hash TEXT NOT NULL DEFAULT '',
  use_kind TEXT NOT NULL DEFAULT '',
  reason TEXT NOT NULL DEFAULT '',
  CONSTRAINT research_artifact_context_entry_representation_check
    CHECK (research_artifact_context_representation_allowed(representation)),
  CONSTRAINT research_artifact_context_entry_manifest_fkey
    FOREIGN KEY (workspace_id, session_id, manifest_id)
    REFERENCES research_artifact_context_manifest (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_context_entry_version_fkey
    FOREIGN KEY (workspace_id, session_id, artifact_version_id)
    REFERENCES research_artifact_version (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_context_entry_ordinal_uidx
    UNIQUE (workspace_id, session_id, manifest_id, ordinal),
  CONSTRAINT research_artifact_context_entry_version_use_uidx
    UNIQUE (workspace_id, session_id, manifest_id, artifact_version_id, representation, use_kind)
);

CREATE TABLE research_artifact_context_omission (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  manifest_id UUID NOT NULL,
  candidate_version_id UUID NOT NULL,
  ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
  reason TEXT NOT NULL,
  CONSTRAINT research_artifact_context_omission_reason_check
    CHECK (research_artifact_context_omission_reason_allowed(reason)),
  CONSTRAINT research_artifact_context_omission_manifest_fkey
    FOREIGN KEY (workspace_id, session_id, manifest_id)
    REFERENCES research_artifact_context_manifest (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_context_omission_version_fkey
    FOREIGN KEY (workspace_id, session_id, candidate_version_id)
    REFERENCES research_artifact_version (workspace_id, session_id, id) ON DELETE CASCADE
);

CREATE TABLE research_artifact_input_reference (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  consumer_version_id UUID NOT NULL,
  input_version_id UUID NOT NULL,
  relation TEXT NOT NULL CHECK (btrim(relation) <> ''),
  manifest_id UUID,
  explicitly_used BOOLEAN NOT NULL DEFAULT true,
  purpose TEXT NOT NULL DEFAULT '',
  ordinal INTEGER NOT NULL DEFAULT 0 CHECK (ordinal >= 0),
  CONSTRAINT research_artifact_input_reference_consumer_fkey
    FOREIGN KEY (workspace_id, session_id, consumer_version_id)
    REFERENCES research_artifact_version (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_input_reference_input_fkey
    FOREIGN KEY (workspace_id, session_id, input_version_id)
    REFERENCES research_artifact_version (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_input_reference_manifest_fkey
    FOREIGN KEY (workspace_id, session_id, manifest_id)
    REFERENCES research_artifact_context_manifest (workspace_id, session_id, id),
  CONSTRAINT research_artifact_input_reference_no_self_check
    CHECK (consumer_version_id <> input_version_id),
  CONSTRAINT research_artifact_input_reference_pair_uidx
    UNIQUE (workspace_id, session_id, consumer_version_id, input_version_id, relation)
);

CREATE TABLE research_artifact_supersession (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  successor_version_id UUID NOT NULL,
  superseded_version_id UUID NOT NULL,
  superseded_artifact_id UUID NOT NULL,
  reason TEXT NOT NULL CHECK (btrim(reason) <> ''),
  decision_id UUID NOT NULL,
  policy_watermark BIGINT NOT NULL CHECK (policy_watermark >= 0),
  old_eligibility_revision BIGINT NOT NULL CHECK (old_eligibility_revision >= 1),
  new_eligibility_revision BIGINT NOT NULL CHECK (new_eligibility_revision >= 1),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT research_artifact_supersession_successor_fkey
    FOREIGN KEY (workspace_id, session_id, successor_version_id)
    REFERENCES research_artifact_version (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_supersession_superseded_fkey
    FOREIGN KEY (workspace_id, session_id, superseded_version_id)
    REFERENCES research_artifact_version (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_supersession_passport_fkey
    FOREIGN KEY (workspace_id, session_id, superseded_artifact_id)
    REFERENCES research_artifact_passport (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_supersession_decision_fkey
    FOREIGN KEY (workspace_id, session_id, decision_id)
    REFERENCES research_decision (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_supersession_no_self_check
    CHECK (successor_version_id <> superseded_version_id),
  CONSTRAINT research_artifact_supersession_pair_uidx
    UNIQUE (workspace_id, session_id, successor_version_id, superseded_version_id)
);

CREATE TABLE research_artifact_lifecycle_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  artifact_id UUID NOT NULL,
  old_status TEXT NOT NULL,
  new_status TEXT NOT NULL,
  old_eligibility_revision BIGINT NOT NULL CHECK (old_eligibility_revision >= 1),
  new_eligibility_revision BIGINT NOT NULL CHECK (new_eligibility_revision >= 1),
  policy_watermark BIGINT NOT NULL CHECK (policy_watermark >= 0),
  decision_id UUID,
  actor_type TEXT NOT NULL DEFAULT 'system',
  actor_id UUID,
  reason TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT research_artifact_lifecycle_event_status_check CHECK (
    research_artifact_lifecycle_status_allowed(old_status)
    AND research_artifact_lifecycle_status_allowed(new_status)
  ),
  CONSTRAINT research_artifact_lifecycle_event_passport_fkey
    FOREIGN KEY (workspace_id, session_id, artifact_id)
    REFERENCES research_artifact_passport (workspace_id, session_id, id) ON DELETE CASCADE,
  CONSTRAINT research_artifact_lifecycle_event_decision_fkey
    FOREIGN KEY (workspace_id, session_id, decision_id)
    REFERENCES research_decision (workspace_id, session_id, id)
);

CREATE TABLE research_artifact_migration_diagnostic (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  owner_kind TEXT NOT NULL CHECK (btrim(owner_kind) <> ''),
  owner_id UUID NOT NULL,
  field_path TEXT NOT NULL DEFAULT '',
  expected_target_kind TEXT NOT NULL DEFAULT '',
  reference_value TEXT NOT NULL DEFAULT '',
  reason_code TEXT NOT NULL CHECK (btrim(reason_code) <> ''),
  detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT research_artifact_migration_diagnostic_session_fkey
    FOREIGN KEY (workspace_id, session_id)
    REFERENCES research_session (workspace_id, id) ON DELETE CASCADE
);

CREATE INDEX research_artifact_migration_diagnostic_owner_idx
  ON research_artifact_migration_diagnostic (workspace_id, session_id, owner_kind, owner_id);

-- ---------------------------------------------------------------------------
-- 4) Immutability and class-table guards
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION research_artifact_version_immutable_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'research artifact version is immutable'
    USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_version_immutable_guard';
END;
$$;

CREATE TRIGGER research_artifact_version_immutable_guard
BEFORE UPDATE OR DELETE ON research_artifact_version
FOR EACH ROW EXECUTE FUNCTION research_artifact_version_immutable_guard();

CREATE OR REPLACE FUNCTION research_artifact_policy_mutation_append_only_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'research artifact policy mutation is append-only'
    USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_policy_mutation_append_only_guard';
END;
$$;

CREATE TRIGGER research_artifact_policy_mutation_append_only_guard
BEFORE UPDATE OR DELETE ON research_artifact_policy_mutation
FOR EACH ROW EXECUTE FUNCTION research_artifact_policy_mutation_append_only_guard();

CREATE OR REPLACE FUNCTION research_artifact_lifecycle_event_append_only_guard()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  RAISE EXCEPTION 'research artifact lifecycle event is append-only'
    USING ERRCODE = '55000', CONSTRAINT = 'research_artifact_lifecycle_event_append_only_guard';
END;
$$;

CREATE TRIGGER research_artifact_lifecycle_event_append_only_guard
BEFORE UPDATE OR DELETE ON research_artifact_lifecycle_event
FOR EACH ROW EXECUTE FUNCTION research_artifact_lifecycle_event_append_only_guard();

CREATE OR REPLACE FUNCTION research_artifact_passport_class_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  CASE NEW.entity_kind
    WHEN 'run_session' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_session s
        WHERE s.workspace_id = NEW.workspace_id AND s.id = NEW.id AND s.id = NEW.session_id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'contract_revision' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_contract_revision r
        WHERE r.workspace_id = NEW.workspace_id AND r.session_id = NEW.session_id AND r.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'method_decision' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_decision d
        WHERE d.workspace_id = NEW.workspace_id AND d.session_id = NEW.session_id AND d.id = NEW.id
          AND d.decision_kind = 'research_method'
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'evaluation_decision' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_decision d
        WHERE d.workspace_id = NEW.workspace_id AND d.session_id = NEW.session_id AND d.id = NEW.id
          AND d.decision_kind <> 'research_method'
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'question' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_question q
        WHERE q.workspace_id = NEW.workspace_id AND q.session_id = NEW.session_id AND q.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'task' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_task t
        WHERE t.workspace_id = NEW.workspace_id AND t.session_id = NEW.session_id AND t.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'attempt' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_task_attempt a
        WHERE a.workspace_id = NEW.workspace_id AND a.session_id = NEW.session_id AND a.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'result_artifact' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_result_artifact r
        WHERE r.workspace_id = NEW.workspace_id AND r.session_id = NEW.session_id AND r.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'legacy_source' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_source s
        WHERE s.workspace_id = NEW.workspace_id AND s.session_id = NEW.session_id AND s.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'source_snapshot' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_source_snapshot s
        WHERE s.workspace_id = NEW.workspace_id AND s.session_id = NEW.session_id AND s.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'observation' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_observation o
        WHERE o.workspace_id = NEW.workspace_id AND o.session_id = NEW.session_id AND o.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'claim' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_claim c
        WHERE c.workspace_id = NEW.workspace_id AND c.session_id = NEW.session_id AND c.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'evidence_link' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_claim_evidence e
        WHERE e.workspace_id = NEW.workspace_id AND e.session_id = NEW.session_id AND e.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'report_revision' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_report r
        WHERE r.workspace_id = NEW.workspace_id AND r.session_id = NEW.session_id AND r.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'stage_evaluation' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_stage_eval e
        WHERE e.workspace_id = NEW.workspace_id AND e.session_id = NEW.session_id AND e.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'research_message' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_message m
        WHERE m.workspace_id = NEW.workspace_id AND m.session_id = NEW.session_id AND m.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'product_round_decision' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_product_round_card p
        WHERE p.workspace_id = NEW.workspace_id AND p.session_id = NEW.session_id AND p.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'context_manifest' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_artifact_context_manifest m
        WHERE m.workspace_id = NEW.workspace_id AND m.session_id = NEW.session_id AND m.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'run_event' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_run_event e
        WHERE e.workspace_id = NEW.workspace_id AND e.session_id = NEW.session_id AND e.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'graph_node' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_graph_node n
        WHERE n.workspace_id = NEW.workspace_id AND n.session_id = NEW.session_id AND n.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    WHEN 'graph_edge' THEN
      IF NOT EXISTS (
        SELECT 1 FROM research_graph_edge e
        WHERE e.workspace_id = NEW.workspace_id AND e.session_id = NEW.session_id AND e.id = NEW.id
      ) THEN RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard'; END IF;
    ELSE
      RAISE foreign_key_violation USING CONSTRAINT = 'research_artifact_passport_class_guard';
  END CASE;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_artifact_passport_class_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, entity_kind ON research_artifact_passport
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_passport_class_guard_fn();

-- Reciprocal domain-table passport guards land with D1 creation wiring (CreateRun et al.).
-- D1 backfill inserts run_session passports without changing existing write paths.

-- ---------------------------------------------------------------------------
-- 5) Session policy state seed + honest run_session passport backfill
-- ---------------------------------------------------------------------------

INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
SELECT workspace_id, id, 'legacy-v1-v5-compat-v1', 0
FROM research_session
ON CONFLICT (workspace_id, session_id) DO NOTHING;

INSERT INTO research_artifact_passport (
  id, workspace_id, session_id, entity_kind, current_version, eligibility_revision,
  lifecycle_status, provenance_completeness, source_created_at, registered_at
)
SELECT
  s.id, s.workspace_id, s.id, 'run_session', NULL, 1,
  'registered', 'partial', s.created_at, now()
FROM research_session s
ON CONFLICT (workspace_id, session_id, id) DO NOTHING;
