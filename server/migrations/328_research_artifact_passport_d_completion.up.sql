-- Chapter D completion (D1 tail): D-enable flag, ledger repair, decision/event parsers, scoped FKs.

ALTER TABLE research_session
  ADD COLUMN IF NOT EXISTS artifact_passport_enabled BOOLEAN NOT NULL DEFAULT false;

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT parser IN (
    'research_message_match_decision',
    'research_decision_inputs',
    'research_run_event_payload'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_scoped_task_reference(
  p_workspace_id UUID,
  p_session_id UUID,
  p_owner_kind TEXT,
  p_owner_id UUID,
  p_field_path TEXT,
  p_reference_value TEXT
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
  IF btrim(COALESCE(p_reference_value, '')) = '' THEN
    RETURN;
  END IF;
  IF NOT research_artifact_reference_uuid_valid(p_reference_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, p_owner_kind, p_owner_id,
      p_field_path, 'task', p_reference_value, 'malformed_uuid'
    );
    RETURN;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM research_task t
    WHERE t.workspace_id = p_workspace_id
      AND t.session_id = p_session_id
      AND t.id = p_reference_value::uuid
  ) THEN
    IF EXISTS (
      SELECT 1 FROM research_task t
      WHERE t.id = p_reference_value::uuid
        AND (t.workspace_id IS DISTINCT FROM p_workspace_id
          OR t.session_id IS DISTINCT FROM p_session_id)
    ) THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, p_owner_kind, p_owner_id,
        p_field_path, 'task', p_reference_value, 'cross_scope_reference'
      );
    ELSE
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, p_owner_kind, p_owner_id,
        p_field_path, 'task', p_reference_value, 'unresolved_reference'
      );
    END IF;
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_scoped_attempt_reference(
  p_workspace_id UUID,
  p_session_id UUID,
  p_owner_kind TEXT,
  p_owner_id UUID,
  p_field_path TEXT,
  p_reference_value TEXT
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
  IF btrim(COALESCE(p_reference_value, '')) = '' THEN
    RETURN;
  END IF;
  IF NOT research_artifact_reference_uuid_valid(p_reference_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, p_owner_kind, p_owner_id,
      p_field_path, 'attempt', p_reference_value, 'malformed_uuid'
    );
    RETURN;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM research_task_attempt a
    WHERE a.workspace_id = p_workspace_id
      AND a.session_id = p_session_id
      AND a.id = p_reference_value::uuid
  ) THEN
    IF EXISTS (
      SELECT 1 FROM research_task_attempt a
      WHERE a.id = p_reference_value::uuid
        AND (a.workspace_id IS DISTINCT FROM p_workspace_id
          OR a.session_id IS DISTINCT FROM p_session_id)
    ) THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, p_owner_kind, p_owner_id,
        p_field_path, 'attempt', p_reference_value, 'cross_scope_reference'
      );
    ELSE
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, p_owner_kind, p_owner_id,
        p_field_path, 'attempt', p_reference_value, 'unresolved_reference'
      );
    END IF;
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_scoped_report_reference(
  p_workspace_id UUID,
  p_session_id UUID,
  p_owner_kind TEXT,
  p_owner_id UUID,
  p_field_path TEXT,
  p_reference_value TEXT
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
  IF btrim(COALESCE(p_reference_value, '')) = '' THEN
    RETURN;
  END IF;
  IF NOT research_artifact_reference_uuid_valid(p_reference_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, p_owner_kind, p_owner_id,
      p_field_path, 'report_revision', p_reference_value, 'malformed_uuid'
    );
    RETURN;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM research_report r
    WHERE r.workspace_id = p_workspace_id
      AND r.session_id = p_session_id
      AND r.id = p_reference_value::uuid
  ) THEN
    IF EXISTS (
      SELECT 1 FROM research_report r
      WHERE r.id = p_reference_value::uuid
        AND (r.workspace_id IS DISTINCT FROM p_workspace_id
          OR r.session_id IS DISTINCT FROM p_session_id)
    ) THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, p_owner_kind, p_owner_id,
        p_field_path, 'report_revision', p_reference_value, 'cross_scope_reference'
      );
    ELSE
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, p_owner_kind, p_owner_id,
        p_field_path, 'report_revision', p_reference_value, 'unresolved_reference'
      );
    END IF;
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_decision_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID,
  p_decision_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_inputs JSONB;
  v_count INTEGER;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id, p_session_id, 'evaluation_decision', p_decision_id
  );
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id, p_session_id, 'method_decision', p_decision_id
  );

  SELECT d.inputs INTO v_inputs
  FROM research_decision d
  WHERE d.workspace_id = p_workspace_id
    AND d.session_id = p_session_id
    AND d.id = p_decision_id;
  IF NOT FOUND OR v_inputs IS NULL OR v_inputs = '{}'::jsonb THEN
    RETURN 0;
  END IF;

  PERFORM research_artifact_diagnose_scoped_task_reference(
    p_workspace_id, p_session_id,
    CASE WHEN (SELECT decision_kind FROM research_decision WHERE id = p_decision_id) = 'research_method'
      THEN 'method_decision' ELSE 'evaluation_decision' END,
    p_decision_id, '/inputs/task_id', v_inputs->>'task_id'
  );
  PERFORM research_artifact_diagnose_scoped_attempt_reference(
    p_workspace_id, p_session_id,
    CASE WHEN (SELECT decision_kind FROM research_decision WHERE id = p_decision_id) = 'research_method'
      THEN 'method_decision' ELSE 'evaluation_decision' END,
    p_decision_id, '/inputs/attempt_id', v_inputs->>'attempt_id'
  );
  PERFORM research_artifact_diagnose_scoped_report_reference(
    p_workspace_id, p_session_id,
    CASE WHEN (SELECT decision_kind FROM research_decision WHERE id = p_decision_id) = 'research_method'
      THEN 'method_decision' ELSE 'evaluation_decision' END,
    p_decision_id, '/inputs/report_id', v_inputs->>'report_id'
  );

  SELECT count(*)::int INTO v_count
  FROM research_artifact_migration_diagnostic d
  WHERE d.workspace_id = p_workspace_id
    AND d.session_id = p_session_id
    AND d.owner_id = p_decision_id;

  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_run_event_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID,
  p_event_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_payload JSONB;
  v_count INTEGER;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id, p_session_id, 'run_event', p_event_id
  );

  SELECT e.payload INTO v_payload
  FROM research_run_event e
  WHERE e.workspace_id = p_workspace_id
    AND e.session_id = p_session_id
    AND e.id = p_event_id;
  IF NOT FOUND OR v_payload IS NULL OR v_payload = '{}'::jsonb THEN
    RETURN 0;
  END IF;

  PERFORM research_artifact_diagnose_scoped_task_reference(
    p_workspace_id, p_session_id, 'run_event', p_event_id,
    '/payload/task_id', v_payload->>'task_id'
  );
  PERFORM research_artifact_diagnose_scoped_attempt_reference(
    p_workspace_id, p_session_id, 'run_event', p_event_id,
    '/payload/attempt_id', v_payload->>'attempt_id'
  );
  PERFORM research_artifact_diagnose_scoped_report_reference(
    p_workspace_id, p_session_id, 'run_event', p_event_id,
    '/payload/report_id', v_payload->>'report_id'
  );

  SELECT count(*)::int INTO v_count
  FROM research_artifact_migration_diagnostic d
  WHERE d.workspace_id = p_workspace_id
    AND d.session_id = p_session_id
    AND d.owner_id = p_event_id;

  RETURN v_count;
END;
$$;

-- Repair missing artifact_create ledger rows for 319 backfill passports.
DO $$
DECLARE
  v_row RECORD;
BEGIN
  FOR v_row IN
    SELECT p.workspace_id, p.session_id, p.id
    FROM research_artifact_passport p
    WHERE p.eligibility_revision = 1
      AND NOT EXISTS (
        SELECT 1 FROM research_artifact_policy_mutation m
        WHERE m.workspace_id = p.workspace_id
          AND m.session_id = p.session_id
          AND m.artifact_id = p.id
          AND m.mutation_kind = 'artifact_create'
          AND m.old_eligibility_revision = 0
          AND m.new_eligibility_revision = 1
      )
  LOOP
    PERFORM research_artifact_record_artifact_create_mutation(
      v_row.workspace_id, v_row.session_id, v_row.id
    );
  END LOOP;
END;
$$;

-- Scoped composite FKs: report_claim and graph_edge endpoints.
ALTER TABLE research_report_claim
  ADD COLUMN IF NOT EXISTS workspace_id UUID,
  ADD COLUMN IF NOT EXISTS session_id UUID;

UPDATE research_report_claim rc
SET workspace_id = r.workspace_id,
    session_id = r.session_id
FROM research_report r
WHERE r.id = rc.report_id
  AND (rc.workspace_id IS NULL OR rc.session_id IS NULL);

ALTER TABLE research_report_claim
  ALTER COLUMN workspace_id SET NOT NULL,
  ALTER COLUMN session_id SET NOT NULL;

ALTER TABLE research_report_claim DROP CONSTRAINT IF EXISTS research_report_claim_pkey;
ALTER TABLE research_report_claim DROP CONSTRAINT IF EXISTS research_report_claim_report_id_fkey;
ALTER TABLE research_report_claim DROP CONSTRAINT IF EXISTS research_report_claim_claim_id_fkey;

ALTER TABLE research_report_claim
  ADD CONSTRAINT research_report_claim_pkey
  PRIMARY KEY (workspace_id, session_id, report_id, claim_id, section_id);

ALTER TABLE research_report_claim
  ADD CONSTRAINT research_report_claim_report_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, report_id)
  REFERENCES research_report (workspace_id, session_id, id) ON DELETE CASCADE;

ALTER TABLE research_report_claim
  ADD CONSTRAINT research_report_claim_claim_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, claim_id)
  REFERENCES research_claim (workspace_id, session_id, id) ON DELETE CASCADE;

ALTER TABLE research_graph_edge DROP CONSTRAINT IF EXISTS research_graph_edge_from_node_id_fkey;
ALTER TABLE research_graph_edge DROP CONSTRAINT IF EXISTS research_graph_edge_to_node_id_fkey;

ALTER TABLE research_graph_edge
  ADD CONSTRAINT research_graph_edge_from_node_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, from_node_id)
  REFERENCES research_graph_node (workspace_id, session_id, id) ON DELETE CASCADE;

ALTER TABLE research_graph_edge
  ADD CONSTRAINT research_graph_edge_to_node_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, to_node_id)
  REFERENCES research_graph_node (workspace_id, session_id, id) ON DELETE CASCADE;
