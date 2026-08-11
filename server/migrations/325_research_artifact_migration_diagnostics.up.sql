-- Chapter D1h: migration diagnostic registry + Research Message reference scan (design §4.8).

CREATE OR REPLACE FUNCTION research_artifact_migration_diagnostic_reason_allowed(reason TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT reason IN (
    'malformed_uuid',
    'unresolved_reference',
    'cross_scope_reference',
    'invalid_match_decision',
    'unknown_schema'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT parser IN ('research_message_match_decision');
$$;

CREATE OR REPLACE FUNCTION research_artifact_bounded_reference_value(value TEXT)
RETURNS TEXT
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT left(btrim(COALESCE(value, '')), 256);
$$;

ALTER TABLE research_artifact_migration_diagnostic
  ADD CONSTRAINT research_artifact_migration_diagnostic_reason_check
  CHECK (research_artifact_migration_diagnostic_reason_allowed(reason_code)),
  ADD CONSTRAINT research_artifact_migration_diagnostic_reference_value_check
  CHECK (char_length(reference_value) <= 256);

CREATE UNIQUE INDEX research_artifact_migration_diagnostic_owner_field_uidx
  ON research_artifact_migration_diagnostic (
    workspace_id, session_id, owner_kind, owner_id, field_path
  );

CREATE OR REPLACE FUNCTION research_artifact_reference_uuid_valid(value TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT btrim(COALESCE(value, '')) ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
$$;

CREATE OR REPLACE FUNCTION research_artifact_record_migration_diagnostic(
  p_workspace_id UUID,
  p_session_id UUID,
  p_owner_kind TEXT,
  p_owner_id UUID,
  p_field_path TEXT,
  p_expected_target_kind TEXT,
  p_reference_value TEXT,
  p_reason_code TEXT
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_artifact_migration_diagnostic_reason_allowed(p_reason_code) THEN
    RAISE EXCEPTION 'unknown migration diagnostic reason %', p_reason_code;
  END IF;
  INSERT INTO research_artifact_migration_diagnostic (
    workspace_id, session_id, owner_kind, owner_id,
    field_path, expected_target_kind, reference_value, reason_code
  ) VALUES (
    p_workspace_id, p_session_id, p_owner_kind, p_owner_id,
    p_field_path, p_expected_target_kind,
    research_artifact_bounded_reference_value(p_reference_value),
    p_reason_code
  )
  ON CONFLICT (workspace_id, session_id, owner_kind, owner_id, field_path)
  DO UPDATE SET
    expected_target_kind = EXCLUDED.expected_target_kind,
    reference_value = EXCLUDED.reference_value,
    reason_code = EXCLUDED.reason_code,
    detected_at = now();
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_clear_owner_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID,
  p_owner_kind TEXT,
  p_owner_id UUID
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
  DELETE FROM research_artifact_migration_diagnostic
  WHERE workspace_id = p_workspace_id
    AND session_id = p_session_id
    AND owner_kind = p_owner_kind
    AND owner_id = p_owner_id;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_scoped_graph_node_reference(
  p_workspace_id UUID,
  p_session_id UUID,
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
      p_workspace_id, p_session_id, 'research_message', p_owner_id,
      p_field_path, 'graph_node', p_reference_value, 'malformed_uuid'
    );
    RETURN;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM research_graph_node n
    WHERE n.workspace_id = p_workspace_id
      AND n.session_id = p_session_id
      AND n.id = p_reference_value::uuid
  ) THEN
    IF EXISTS (
      SELECT 1 FROM research_graph_node n
      WHERE n.id = p_reference_value::uuid
        AND (n.workspace_id IS DISTINCT FROM p_workspace_id
          OR n.session_id IS DISTINCT FROM p_session_id)
    ) THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'research_message', p_owner_id,
        p_field_path, 'graph_node', p_reference_value, 'cross_scope_reference'
      );
    ELSE
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'research_message', p_owner_id,
        p_field_path, 'graph_node', p_reference_value, 'unresolved_reference'
      );
    END IF;
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_scoped_message_reference(
  p_workspace_id UUID,
  p_session_id UUID,
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
      p_workspace_id, p_session_id, 'research_message', p_owner_id,
      p_field_path, 'research_message', p_reference_value, 'malformed_uuid'
    );
    RETURN;
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM research_message m
    WHERE m.workspace_id = p_workspace_id
      AND m.session_id = p_session_id
      AND m.id = p_reference_value::uuid
  ) THEN
    IF EXISTS (
      SELECT 1 FROM research_message m
      WHERE m.id = p_reference_value::uuid
        AND (m.workspace_id IS DISTINCT FROM p_workspace_id
          OR m.session_id IS DISTINCT FROM p_session_id)
    ) THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'research_message', p_owner_id,
        p_field_path, 'research_message', p_reference_value, 'cross_scope_reference'
      );
    ELSE
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'research_message', p_owner_id,
        p_field_path, 'research_message', p_reference_value, 'unresolved_reference'
      );
    END IF;
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_message_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID,
  p_message_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_meta JSONB;
  v_match JSONB;
  v_utterance TEXT;
  v_primary TEXT;
  v_nodes JSONB;
  v_decisions JSONB;
  v_idx INTEGER;
  v_count INTEGER;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id, p_session_id, 'research_message', p_message_id
  );

  SELECT m.meta INTO v_meta
  FROM research_message m
  WHERE m.workspace_id = p_workspace_id
    AND m.session_id = p_session_id
    AND m.id = p_message_id;

  IF NOT FOUND OR v_meta IS NULL OR v_meta = '{}'::jsonb THEN
    RETURN 0;
  END IF;

  v_match := v_meta->'match_decision';
  IF v_match IS NULL OR v_match = 'null'::jsonb OR jsonb_typeof(v_match) <> 'object' THEN
    RETURN 0;
  END IF;

  v_utterance := btrim(COALESCE(v_match->>'utterance_id', ''));
  IF v_utterance = '' THEN
    v_utterance := p_message_id::text;
  ELSE
    PERFORM research_artifact_diagnose_scoped_message_reference(
      p_workspace_id, p_session_id, p_message_id,
      '/meta/match_decision/utterance_id', v_utterance
    );
  END IF;

  v_primary := btrim(COALESCE(v_match->>'primary_anchor_node_id', ''));
  IF v_primary <> '' THEN
    PERFORM research_artifact_diagnose_scoped_graph_node_reference(
      p_workspace_id, p_session_id, p_message_id,
      '/meta/match_decision/primary_anchor_node_id', v_primary
    );
  END IF;

  v_nodes := COALESCE(v_match->'matched_node_ids', '[]'::jsonb);
  IF jsonb_typeof(v_nodes) = 'array' THEN
    FOR v_idx IN 0 .. jsonb_array_length(v_nodes) - 1 LOOP
      PERFORM research_artifact_diagnose_scoped_graph_node_reference(
        p_workspace_id, p_session_id, p_message_id,
        '/meta/match_decision/matched_node_ids/' || v_idx::text,
        v_nodes->>v_idx
      );
    END LOOP;
  ELSE
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, 'research_message', p_message_id,
      '/meta/match_decision/matched_node_ids', 'graph_node', '', 'invalid_match_decision'
    );
  END IF;

  v_decisions := COALESCE(v_match->'decisions', '[]'::jsonb);
  IF jsonb_typeof(v_decisions) = 'array' THEN
    FOR v_idx IN 0 .. jsonb_array_length(v_decisions) - 1 LOOP
      PERFORM research_artifact_diagnose_scoped_graph_node_reference(
        p_workspace_id, p_session_id, p_message_id,
        '/meta/match_decision/decisions/' || v_idx::text || '/node_id',
        v_decisions->v_idx->>'node_id'
      );
    END LOOP;
  ELSE
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, 'research_message', p_message_id,
      '/meta/match_decision/decisions', 'graph_node', '', 'invalid_match_decision'
    );
  END IF;

  SELECT count(*)::int INTO v_count
  FROM research_artifact_migration_diagnostic d
  WHERE d.workspace_id = p_workspace_id
    AND d.session_id = p_session_id
    AND d.owner_kind = 'research_message'
    AND d.owner_id = p_message_id;

  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_session_message_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_message_id UUID;
  v_total INTEGER := 0;
  v_count INTEGER;
BEGIN
  FOR v_message_id IN
    SELECT m.id
    FROM research_message m
    WHERE m.workspace_id = p_workspace_id
      AND m.session_id = p_session_id
      AND m.meta ? 'match_decision'
  LOOP
    v_count := research_artifact_scan_research_message_migration_diagnostics(
      p_workspace_id, p_session_id, v_message_id
    );
    v_total := v_total + v_count;
  END LOOP;
  RETURN v_total;
END;
$$;
