-- Chapter D §4.8: backfill report-local Evaluation Decision lineage.

CREATE OR REPLACE FUNCTION research_artifact_insert_evaluation_claim_key_reference(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID,p_report_id UUID,
  p_claim_key TEXT,p_relation TEXT,p_field_path TEXT
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_claim_id UUID; v_matches INTEGER;
BEGIN
  SELECT count(DISTINCT claim.id)::int,min(claim.id::text)::uuid
  INTO v_matches,v_claim_id
  FROM research_report_claim link
  JOIN research_claim claim
    ON claim.workspace_id=link.workspace_id AND claim.session_id=link.session_id
   AND claim.id=link.claim_id
  WHERE link.workspace_id=p_workspace_id AND link.session_id=p_session_id
    AND link.report_id=p_report_id AND claim.client_key=p_claim_key;
  IF v_matches<>1 THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'evaluation_decision',p_decision_id,p_field_path,
      'report_claim',p_claim_key,
      CASE WHEN v_matches=0 THEN 'dangling_local_key' ELSE 'ambiguous_local_key' END
    );
    RETURN 1;
  END IF;
  RETURN research_artifact_insert_decision_reference(
    p_workspace_id,p_session_id,p_decision_id,'evaluation_decision',v_claim_id,
    'claim',p_relation,0,p_field_path
  );
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_evaluation_claim_key_array(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID,p_report_id UUID,
  p_values JSONB,p_relation TEXT,p_field_path TEXT
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_value JSONB; v_ordinal BIGINT; v_total INTEGER := 0;
BEGIN
  IF p_values IS NULL OR p_values='null'::jsonb THEN RETURN 0; END IF;
  FOR v_value,v_ordinal IN
    SELECT value,ordinality FROM jsonb_array_elements(p_values) WITH ORDINALITY
  LOOP
    v_total:=v_total+research_artifact_insert_evaluation_claim_key_reference(
      p_workspace_id,p_session_id,p_decision_id,p_report_id,v_value#>>'{}',
      p_relation,p_field_path||'/'||(v_ordinal-1)
    );
  END LOOP;
  RETURN v_total;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_evaluation_local_references_if_clean(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE
  v_kind TEXT; v_inputs JSONB; v_outcome JSONB; v_report_id UUID;
  v_defect JSONB; v_ordinal BIGINT; v_base TEXT; v_diagnostics INTEGER;
  v_total INTEGER := 0; v_has_reviewed_sections BOOLEAN := false;
  v_has_defect_sections BOOLEAN := false;
BEGIN
  SELECT count(*)::int INTO v_diagnostics
  FROM research_artifact_migration_diagnostic
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    AND owner_kind='evaluation_decision' AND owner_id=p_decision_id;
  IF v_diagnostics>0 THEN RETURN 0; END IF;
  SELECT decision_kind,COALESCE(inputs,'{}'::jsonb),COALESCE(outcome,'{}'::jsonb)
  INTO v_kind,v_inputs,v_outcome
  FROM research_decision
  WHERE workspace_id=p_workspace_id AND session_id=p_session_id AND id=p_decision_id;
  IF NOT FOUND OR v_kind NOT IN ('quality_gate','citation_audit') THEN RETURN 0; END IF;
  v_report_id:=(v_inputs->>'report_id')::uuid;
  v_total:=v_total+research_artifact_materialize_evaluation_claim_key_array(
    p_workspace_id,p_session_id,p_decision_id,v_report_id,
    v_outcome->'reviewed_claim_keys','decision_reviewed_claim','/outcome/reviewed_claim_keys'
  );
  v_has_reviewed_sections:=jsonb_typeof(v_outcome->'reviewed_section_ids')='array'
    AND jsonb_array_length(v_outcome->'reviewed_section_ids')>0;
  IF jsonb_typeof(v_outcome->'defects')='array' THEN
    FOR v_defect,v_ordinal IN
      SELECT value,ordinality FROM jsonb_array_elements(v_outcome->'defects') WITH ORDINALITY
    LOOP
      v_base:='/outcome/defects/'||(v_ordinal-1);
      v_total:=v_total+research_artifact_materialize_evaluation_claim_key_array(
        p_workspace_id,p_session_id,p_decision_id,v_report_id,
        v_defect->'claim_keys','decision_defect_claim',v_base||'/claim_keys'
      );
      v_has_defect_sections:=v_has_defect_sections OR (
        jsonb_typeof(v_defect->'section_ids')='array'
        AND jsonb_array_length(v_defect->'section_ids')>0
      );
    END LOOP;
  END IF;
  IF v_has_reviewed_sections THEN
    v_total:=v_total+research_artifact_insert_decision_reference(
      p_workspace_id,p_session_id,p_decision_id,'evaluation_decision',v_report_id,
      'report_revision','decision_reviewed_report_section',0,'/outcome/reviewed_section_ids'
    );
  END IF;
  IF v_has_defect_sections THEN
    v_total:=v_total+research_artifact_insert_decision_reference(
      p_workspace_id,p_session_id,p_decision_id,'evaluation_decision',v_report_id,
      'report_revision','decision_defect_report_section',0,'/outcome/defects/*/section_ids'
    );
  END IF;
  RETURN v_total;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_materialize_evaluation_local_references(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_diagnostics INTEGER;
BEGIN
  v_diagnostics:=research_artifact_scan_research_decision_migration_diagnostics(
    p_workspace_id,p_session_id,p_decision_id
  );
  v_diagnostics:=v_diagnostics+research_artifact_scan_research_evaluation_local_diagnostics(
    p_workspace_id,p_session_id,p_decision_id
  );
  IF v_diagnostics>0 THEN RETURN v_diagnostics; END IF;
  RETURN research_artifact_materialize_evaluation_local_references_if_clean(
    p_workspace_id,p_session_id,p_decision_id
  );
END;
$$;

ALTER FUNCTION research_artifact_scan_session_migration_diagnostics(UUID,UUID)
  RENAME TO research_artifact_scan_session_migration_diagnostics_v382;

CREATE FUNCTION research_artifact_scan_session_migration_diagnostics(
  p_workspace_id UUID,p_session_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_owner_id UUID; v_total INTEGER;
BEGIN
  v_total:=research_artifact_scan_session_migration_diagnostics_v382(p_workspace_id,p_session_id);
  FOR v_owner_id IN
    SELECT id FROM research_decision
    WHERE workspace_id=p_workspace_id AND session_id=p_session_id
      AND decision_kind IN ('quality_gate','citation_audit')
  LOOP
    v_total:=v_total+research_artifact_materialize_evaluation_local_references_if_clean(
      p_workspace_id,p_session_id,v_owner_id
    );
  END LOOP;
  RETURN v_total;
END;
$$;

DO $$ DECLARE v_decision RECORD; BEGIN
  FOR v_decision IN
    SELECT workspace_id,session_id,id FROM research_decision
    WHERE decision_kind IN ('quality_gate','citation_audit')
  LOOP
    PERFORM research_artifact_materialize_evaluation_local_references(
      v_decision.workspace_id,v_decision.session_id,v_decision.id
    );
  END LOOP;
END $$;
