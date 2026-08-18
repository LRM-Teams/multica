ALTER TABLE research_work_item_attempt DROP CONSTRAINT research_v6_attempt_result_artifact_fkey;
DROP INDEX research_v6_derivation_exact_idx;
ALTER TABLE research_insight_derivation
  DROP CONSTRAINT research_v6_derivation_round_fkey,
  DROP CONSTRAINT research_v6_derivation_input_version_fkey,
  DROP CONSTRAINT research_v6_derivation_output_version_fkey,
  DROP CONSTRAINT research_v6_derivation_shape_check,
  DROP COLUMN input_tier,
  DROP COLUMN integration_round_id,
  DROP COLUMN input_artifact_version_id,
  DROP COLUMN insight_version_id,
  ALTER COLUMN input_content_hash SET NOT NULL,
  ALTER COLUMN input_entity_id SET NOT NULL,
  ALTER COLUMN input_kind SET NOT NULL;
ALTER TABLE research_node_steward_assignment DROP CONSTRAINT research_v6_steward_version_fkey;
ALTER TABLE research_node_absorption DROP CONSTRAINT research_v6_absorption_input_version_fkey;
ALTER TABLE research_branch_frontier DROP CONSTRAINT research_v6_frontier_version_fkey;
ALTER TABLE research_node_branch DROP CONSTRAINT research_v6_node_branch_version_fkey;
ALTER TABLE research_insight_version DROP CONSTRAINT research_v6_insight_artifact_version_fkey;
ALTER TABLE research_result_node DROP CONSTRAINT research_v6_result_node_version_fkey,
  DROP CONSTRAINT research_v6_result_node_artifact_fkey;

DROP TRIGGER research_result_attempt_projection_guard ON research_result_artifact;
DROP TRIGGER research_result_artifact_v6_binding_immutable_guard ON research_result_artifact;
DROP FUNCTION research_result_artifact_v6_binding_immutable_guard_fn();
DROP TRIGGER research_artifact_version_producer_guard ON research_artifact_version;

ALTER TABLE research_result_artifact
  DROP CONSTRAINT research_result_artifact_v6_manifest_binding_check,
  DROP CONSTRAINT research_result_artifact_one_attempt_origin_check,
  DROP CONSTRAINT research_result_artifact_v6_attempt_fkey,
  DROP COLUMN acceptance_lineage_v6,
  DROP COLUMN resolved_input_versions_v6,
  DROP COLUMN acceptance_work_manifest_hash,
  DROP COLUMN acceptance_work_manifest_id,
  DROP COLUMN work_item_attempt_id,
  ALTER COLUMN attempt_id SET NOT NULL;
ALTER TABLE research_artifact_version
  DROP CONSTRAINT research_artifact_version_one_execution_origin_check,
  DROP CONSTRAINT research_artifact_version_v6_attempt_fkey,
  DROP CONSTRAINT research_artifact_version_v6_work_item_fkey,
  DROP COLUMN produced_by_work_item_attempt_id,
  DROP COLUMN produced_by_work_item_id;

CREATE OR REPLACE FUNCTION research_artifact_version_producer_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_attempt research_task_attempt%ROWTYPE;
BEGIN
  IF NEW.produced_by_attempt_id IS NULL OR NEW.produced_by_task_id IS NULL THEN RETURN NEW; END IF;
  SELECT * INTO v_attempt FROM research_task_attempt a
    WHERE a.workspace_id=NEW.workspace_id AND a.session_id=NEW.session_id AND a.id=NEW.produced_by_attempt_id;
  IF NOT FOUND OR v_attempt.task_id IS DISTINCT FROM NEW.produced_by_task_id THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
  END IF;
  IF NEW.produced_by_agent_id IS NOT NULL AND NEW.produced_by_agent_id IS DISTINCT FROM v_attempt.assigned_agent_id THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
  END IF;
  IF btrim(NEW.model)<>'' AND NEW.model IS DISTINCT FROM v_attempt.model THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
  END IF;
  IF btrim(NEW.provider)<>'' AND NEW.provider IS DISTINCT FROM v_attempt.provider THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
  END IF;
  IF btrim(NEW.execution_adapter)<>'' AND NEW.execution_adapter IS DISTINCT FROM v_attempt.execution_adapter THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
  END IF;
  RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER research_artifact_version_producer_guard
AFTER INSERT OR UPDATE OF produced_by_task_id,produced_by_attempt_id,produced_by_agent_id,model,provider,execution_adapter
ON research_artifact_version DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_version_producer_guard_fn();

CREATE OR REPLACE FUNCTION research_result_attempt_projection_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v_attempt research_task_attempt%ROWTYPE;
BEGIN
  SELECT * INTO v_attempt FROM research_task_attempt a
    WHERE a.workspace_id=NEW.workspace_id AND a.session_id=NEW.session_id AND a.id=NEW.attempt_id;
  IF NOT FOUND OR v_attempt.result_hash IS NULL OR btrim(v_attempt.result_hash)=''
     OR NEW.content_hash IS DISTINCT FROM v_attempt.result_hash
     OR NEW.client_request_id IS DISTINCT FROM COALESCE(v_attempt.client_request_id,'') THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_result_attempt_projection_guard';
  END IF;
  RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER research_result_attempt_projection_guard
AFTER INSERT OR UPDATE OF workspace_id,session_id,attempt_id,content_hash,client_request_id
ON research_result_artifact DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_result_attempt_projection_guard_fn();
