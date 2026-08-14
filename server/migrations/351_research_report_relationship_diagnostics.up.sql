-- Chapter D1: diagnose structured Report references that cannot use relational FKs.

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
    'unknown_schema',
    'duplicate_local_key',
    'ambiguous_local_key',
    'cyclic_local_reference',
    'dangling_local_key'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT parser IN (
    'research_message_match_decision',
    'research_decision_inputs',
    'research_report_structured',
    'research_run_event_payload'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_report_local_reference(
  p_workspace_id UUID,
  p_session_id UUID,
  p_report_id UUID,
  p_field_path TEXT,
  p_expected_target_kind TEXT,
  p_reference_value TEXT,
  p_match_count BIGINT
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
  IF btrim(COALESCE(p_reference_value, '')) = '' OR p_match_count = 0 THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, 'report_revision', p_report_id,
      p_field_path, p_expected_target_kind, p_reference_value, 'dangling_local_key'
    );
  ELSIF p_match_count > 1 THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, 'report_revision', p_report_id,
      p_field_path, p_expected_target_kind, p_reference_value, 'ambiguous_local_key'
    );
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_report_source_reference(
  p_workspace_id UUID,
  p_session_id UUID,
  p_report_id UUID,
  p_field_path TEXT,
  p_reference_value TEXT
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
  IF NOT research_artifact_reference_uuid_valid(p_reference_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, 'report_revision', p_report_id,
      p_field_path, 'legacy_research_source', p_reference_value, 'malformed_uuid'
    );
    RETURN;
  END IF;
  IF EXISTS (
    SELECT 1 FROM research_source source
    WHERE source.workspace_id = p_workspace_id
      AND source.session_id = p_session_id
      AND source.id = p_reference_value::uuid
  ) THEN
    RETURN;
  END IF;
  IF EXISTS (
    SELECT 1 FROM research_source source
    WHERE source.id = p_reference_value::uuid
      AND (source.workspace_id IS DISTINCT FROM p_workspace_id
        OR source.session_id IS DISTINCT FROM p_session_id)
  ) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, 'report_revision', p_report_id,
      p_field_path, 'legacy_research_source', p_reference_value, 'cross_scope_reference'
    );
  ELSE
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, 'report_revision', p_report_id,
      p_field_path, 'legacy_research_source', p_reference_value, 'unresolved_reference'
    );
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_report_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID,
  p_report_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_structured JSONB;
  v_item JSONB;
  v_idx INTEGER;
  v_child_idx INTEGER;
  v_reference TEXT;
  v_matches BIGINT;
  v_count INTEGER;
  v_claim RECORD;
  v_cycle TEXT;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id, p_session_id, 'report_revision', p_report_id
  );

  SELECT report.structured INTO v_structured
  FROM research_report report
  WHERE report.workspace_id = p_workspace_id
    AND report.session_id = p_session_id
    AND report.id = p_report_id;
  IF NOT FOUND OR v_structured IS NULL OR v_structured = '{}'::jsonb THEN
    RETURN 0;
  END IF;
  IF jsonb_typeof(v_structured) <> 'object'
    OR v_structured->>'schema_version' <> '1'
    OR jsonb_typeof(v_structured->'outline') IS DISTINCT FROM 'array'
    OR jsonb_typeof(v_structured->'sections') IS DISTINCT FROM 'array'
    OR jsonb_typeof(v_structured->'citations') IS DISTINCT FROM 'array'
    OR jsonb_typeof(v_structured->'sources') IS DISTINCT FROM 'array'
  THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, 'report_revision', p_report_id,
      '/structured', 'structured_report_v1', v_structured->>'schema_version', 'unknown_schema'
    );
    RETURN 1;
  END IF;


  FOR v_idx IN 0 .. jsonb_array_length(v_structured->'sections') - 1 LOOP
    v_item := v_structured->'sections'->v_idx;
    v_reference := btrim(COALESCE(v_item->>'id', ''));
    SELECT count(*) INTO v_matches
    FROM jsonb_array_elements(v_structured->'sections') candidate
    WHERE candidate->>'id' = v_reference;
    IF v_reference = '' THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'report_revision', p_report_id,
        '/structured/sections/' || v_idx || '/id', 'report_section', v_reference, 'dangling_local_key'
      );
    ELSIF v_matches > 1 THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'report_revision', p_report_id,
        '/structured/sections/' || v_idx || '/id', 'report_section', v_reference, 'duplicate_local_key'
      );
    ELSE
      SELECT count(*) INTO v_matches
      FROM jsonb_array_elements(v_structured->'outline') candidate
      WHERE candidate->>'id' = v_reference;
      IF v_matches = 0 THEN
        PERFORM research_artifact_record_migration_diagnostic(
          p_workspace_id, p_session_id, 'report_revision', p_report_id,
          '/structured/sections/' || v_idx || '/id', 'report_outline_item', v_reference, 'dangling_local_key'
        );
      END IF;
    END IF;
  END LOOP;

  FOR v_idx IN 0 .. jsonb_array_length(v_structured->'outline') - 1 LOOP
    v_item := v_structured->'outline'->v_idx;
    v_reference := v_item->>'id';
    SELECT count(*) INTO v_matches
    FROM jsonb_array_elements(v_structured->'outline') candidate
    WHERE candidate->>'id' = v_reference;
    IF btrim(COALESCE(v_reference, '')) <> '' AND v_matches > 1 THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'report_revision', p_report_id,
        '/structured/outline/' || v_idx || '/id', 'report_outline_item', v_reference, 'duplicate_local_key'
      );
    END IF;
    SELECT count(*) INTO v_matches
    FROM jsonb_array_elements(v_structured->'sections') section
    WHERE section->>'id' = v_reference;
    PERFORM research_artifact_diagnose_report_local_reference(
      p_workspace_id, p_session_id, p_report_id,
      '/structured/outline/' || v_idx || '/id', 'report_section', v_reference, v_matches
    );
    IF jsonb_typeof(v_item->'children') IS DISTINCT FROM 'array' THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'report_revision', p_report_id,
        '/structured/outline/' || v_idx || '/children', 'report_section', '', 'unknown_schema'
      );
    ELSE
      FOR v_child_idx IN 0 .. jsonb_array_length(v_item->'children') - 1 LOOP
        v_reference := v_item->'children'->>v_child_idx;
        SELECT count(*) INTO v_matches
        FROM jsonb_array_elements(v_structured->'sections') section
        WHERE section->>'id' = v_reference;
        PERFORM research_artifact_diagnose_report_local_reference(
          p_workspace_id, p_session_id, p_report_id,
          '/structured/outline/' || v_idx || '/children/' || v_child_idx,
          'report_section', v_reference, v_matches
        );
        IF (
          SELECT count(*)
          FROM jsonb_array_elements_text(v_item->'children') AS child(value)
          WHERE child.value = v_reference
        ) > 1 THEN
          PERFORM research_artifact_record_migration_diagnostic(
            p_workspace_id, p_session_id, 'report_revision', p_report_id,
            '/structured/outline/' || v_idx || '/children/' || v_child_idx,
            'report_section', v_reference, 'duplicate_local_key'
          );
        END IF;
      END LOOP;
    END IF;
  END LOOP;

  WITH RECURSIVE edges AS (
    SELECT parent->>'id' AS parent_id, child.value AS child_id
    FROM jsonb_array_elements(v_structured->'outline') parent
    CROSS JOIN LATERAL jsonb_array_elements_text(
      CASE WHEN jsonb_typeof(parent->'children') = 'array' THEN parent->'children' ELSE '[]'::jsonb END
    ) AS child(value)
  ), walk(root_id, node_id, path, cyclic) AS (
    SELECT edge.parent_id, edge.child_id, ARRAY[edge.parent_id, edge.child_id], edge.child_id = edge.parent_id
    FROM edges edge
    UNION ALL
    SELECT walk.root_id, edge.child_id, walk.path || edge.child_id, edge.child_id = ANY(walk.path)
    FROM walk
    JOIN edges edge ON edge.parent_id = walk.node_id
    WHERE NOT walk.cyclic
  )
  SELECT root_id INTO v_cycle FROM walk WHERE cyclic LIMIT 1;
  IF v_cycle IS NOT NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, 'report_revision', p_report_id,
      '/structured/outline', 'report_section', v_cycle, 'cyclic_local_reference'
    );
  END IF;

  FOR v_item IN
    SELECT jsonb_build_object('child_id', edge.child_id, 'parent_count', count(DISTINCT edge.parent_id))
    FROM (
      SELECT parent->>'id' AS parent_id, child.value AS child_id
      FROM jsonb_array_elements(v_structured->'outline') parent
      CROSS JOIN LATERAL jsonb_array_elements_text(
        CASE WHEN jsonb_typeof(parent->'children') = 'array' THEN parent->'children' ELSE '[]'::jsonb END
      ) AS child(value)
    ) edge
    GROUP BY edge.child_id
    HAVING count(DISTINCT edge.parent_id) > 1
  LOOP
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id, p_session_id, 'report_revision', p_report_id,
      '/structured/outline/parents/' || (v_item->>'child_id'),
      'report_section', v_item->>'child_id', 'ambiguous_local_key'
    );
  END LOOP;


  FOR v_idx IN 0 .. jsonb_array_length(v_structured->'citations') - 1 LOOP
    v_item := v_structured->'citations'->v_idx;
    v_reference := btrim(COALESCE(v_item->>'id', ''));
    SELECT count(*) INTO v_matches
    FROM jsonb_array_elements(v_structured->'citations') candidate
    WHERE candidate->>'id' = v_reference;
    IF v_reference = '' THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'report_revision', p_report_id,
        '/structured/citations/' || v_idx || '/id', 'report_citation', v_reference, 'dangling_local_key'
      );
    ELSIF v_matches > 1 THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'report_revision', p_report_id,
        '/structured/citations/' || v_idx || '/id', 'report_citation', v_reference, 'duplicate_local_key'
      );
    END IF;
  END LOOP;


  FOR v_idx IN 0 .. jsonb_array_length(v_structured->'sources') - 1 LOOP
    v_item := v_structured->'sources'->v_idx;
    v_reference := btrim(COALESCE(v_item->>'source_id', ''));
    SELECT count(*) INTO v_matches
    FROM jsonb_array_elements(v_structured->'sources') candidate
    WHERE candidate->>'source_id' = v_reference;
    IF v_reference <> '' AND v_matches > 1 THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'report_revision', p_report_id,
        '/structured/sources/' || v_idx || '/source_id', 'structured_source', v_reference, 'duplicate_local_key'
      );
    ELSE
      PERFORM research_artifact_diagnose_report_source_reference(
        p_workspace_id, p_session_id, p_report_id,
        '/structured/sources/' || v_idx || '/source_id', v_reference
      );
    END IF;
  END LOOP;

  FOR v_idx IN 0 .. jsonb_array_length(v_structured->'citations') - 1 LOOP
    v_item := v_structured->'citations'->v_idx;
    v_reference := v_item->>'source_id';
    SELECT count(*) INTO v_matches
    FROM jsonb_array_elements(v_structured->'sources') source
    WHERE source->>'source_id' = v_reference;
    PERFORM research_artifact_diagnose_report_local_reference(
      p_workspace_id, p_session_id, p_report_id,
      '/structured/citations/' || v_idx || '/source_id', 'structured_source', v_reference, v_matches
    );
  END LOOP;

  FOR v_idx IN 0 .. jsonb_array_length(v_structured->'sections') - 1 LOOP
    v_item := v_structured->'sections'->v_idx;
    IF jsonb_typeof(v_item->'citation_ids') IS DISTINCT FROM 'array' THEN
      PERFORM research_artifact_record_migration_diagnostic(
        p_workspace_id, p_session_id, 'report_revision', p_report_id,
        '/structured/sections/' || v_idx || '/citation_ids', 'report_citation', '', 'unknown_schema'
      );
    ELSE
      FOR v_child_idx IN 0 .. jsonb_array_length(v_item->'citation_ids') - 1 LOOP
        v_reference := v_item->'citation_ids'->>v_child_idx;
        SELECT count(*) INTO v_matches
        FROM jsonb_array_elements(v_structured->'citations') citation
        WHERE citation->>'id' = v_reference;
        PERFORM research_artifact_diagnose_report_local_reference(
          p_workspace_id, p_session_id, p_report_id,
          '/structured/sections/' || v_idx || '/citation_ids/' || v_child_idx,
          'report_citation', v_reference, v_matches
        );
      END LOOP;
    END IF;
  END LOOP;

  FOR v_claim IN
    SELECT claim.claim_id, claim.section_id
    FROM research_report_claim claim
    WHERE claim.workspace_id = p_workspace_id
      AND claim.session_id = p_session_id
      AND claim.report_id = p_report_id
  LOOP
    SELECT count(*) INTO v_matches
    FROM jsonb_array_elements(v_structured->'sections') section
    WHERE section->>'id' = v_claim.section_id;
    PERFORM research_artifact_diagnose_report_local_reference(
      p_workspace_id, p_session_id, p_report_id,
      '/report_claim/' || v_claim.claim_id::text || '/section_id',
      'report_section', v_claim.section_id, v_matches
    );
  END LOOP;

  SELECT count(*)::int INTO v_count
  FROM research_artifact_migration_diagnostic diagnostic
  WHERE diagnostic.workspace_id = p_workspace_id
    AND diagnostic.session_id = p_session_id
    AND diagnostic.owner_kind = 'report_revision'
    AND diagnostic.owner_id = p_report_id;
  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_session_report_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_report_id UUID;
  v_total INTEGER := 0;
BEGIN
  DELETE FROM research_artifact_migration_diagnostic diagnostic
  WHERE diagnostic.workspace_id = p_workspace_id
    AND diagnostic.session_id = p_session_id
    AND diagnostic.owner_kind = 'report_revision';
  FOR v_report_id IN
    SELECT report.id
    FROM research_report report
    WHERE report.workspace_id = p_workspace_id
      AND report.session_id = p_session_id
      AND report.structured <> '{}'::jsonb
    ORDER BY report.id
  LOOP
    v_total := v_total + research_artifact_scan_research_report_migration_diagnostics(
      p_workspace_id, p_session_id, v_report_id
    );
  END LOOP;
  RETURN v_total;
END;
$$;
