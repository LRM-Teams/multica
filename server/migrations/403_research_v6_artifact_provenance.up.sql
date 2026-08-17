-- Give the shared Artifact Passport an explicit V6 producer identity. V6 Work
-- Item Attempts are not legacy research_task_attempt rows and must never be
-- projected through that weaker execution model.

ALTER TABLE research_artifact_version
  ADD COLUMN produced_by_work_item_id UUID,
  ADD COLUMN produced_by_work_item_attempt_id UUID,
  ADD CONSTRAINT research_artifact_version_v6_work_item_fkey
    FOREIGN KEY (produced_by_work_item_id) REFERENCES research_work_item(id),
  ADD CONSTRAINT research_artifact_version_v6_attempt_fkey
    FOREIGN KEY (workspace_id,session_id,produced_by_work_item_attempt_id)
    REFERENCES research_work_item_attempt(workspace_id,session_id,id),
  ADD CONSTRAINT research_artifact_version_one_execution_origin_check CHECK (
    (produced_by_work_item_attempt_id IS NULL AND produced_by_work_item_id IS NULL)
    OR
    (produced_by_work_item_attempt_id IS NOT NULL AND produced_by_work_item_id IS NOT NULL
      AND produced_by_attempt_id IS NULL AND produced_by_task_id IS NULL)
  );

ALTER TABLE research_result_artifact
  ALTER COLUMN attempt_id DROP NOT NULL,
  ADD COLUMN work_item_attempt_id UUID,
  ADD COLUMN acceptance_work_manifest_id UUID,
  ADD COLUMN acceptance_work_manifest_hash TEXT,
  ADD COLUMN resolved_input_versions_v6 JSONB,
  ADD COLUMN acceptance_lineage_v6 JSONB,
  ADD CONSTRAINT research_result_artifact_v6_attempt_fkey
    FOREIGN KEY (workspace_id,session_id,work_item_attempt_id)
    REFERENCES research_work_item_attempt(workspace_id,session_id,id) ON DELETE CASCADE,
  ADD CONSTRAINT research_result_artifact_one_attempt_origin_check
    CHECK (num_nonnulls(attempt_id,work_item_attempt_id)=1),
  ADD CONSTRAINT research_result_artifact_v6_manifest_binding_check CHECK (
    (work_item_attempt_id IS NULL AND acceptance_work_manifest_id IS NULL AND acceptance_work_manifest_hash IS NULL
      AND resolved_input_versions_v6 IS NULL AND acceptance_lineage_v6 IS NULL)
    OR
    (work_item_attempt_id IS NOT NULL AND acceptance_work_manifest_id IS NOT NULL
      AND research_artifact_content_hash_valid(acceptance_work_manifest_hash)
      AND jsonb_typeof(resolved_input_versions_v6)='array'
      AND jsonb_typeof(acceptance_lineage_v6)='array')
  );

CREATE OR REPLACE FUNCTION research_result_artifact_v6_binding_immutable_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.acceptance_work_manifest_id IS NOT NULL AND (
    NEW.work_item_attempt_id IS DISTINCT FROM OLD.work_item_attempt_id OR
    NEW.acceptance_work_manifest_id IS DISTINCT FROM OLD.acceptance_work_manifest_id OR
    NEW.acceptance_work_manifest_hash IS DISTINCT FROM OLD.acceptance_work_manifest_hash OR
    NEW.resolved_input_versions_v6 IS DISTINCT FROM OLD.resolved_input_versions_v6 OR
    NEW.acceptance_lineage_v6 IS DISTINCT FROM OLD.acceptance_lineage_v6
  ) THEN RAISE EXCEPTION 'research V6 result binding is immutable' USING ERRCODE='55000'; END IF;
  RETURN NEW;
END;
$$;
CREATE TRIGGER research_result_artifact_v6_binding_immutable_guard
BEFORE UPDATE OF work_item_attempt_id,acceptance_work_manifest_id,acceptance_work_manifest_hash,resolved_input_versions_v6,acceptance_lineage_v6
ON research_result_artifact FOR EACH ROW EXECUTE FUNCTION research_result_artifact_v6_binding_immutable_guard_fn();

DROP TRIGGER research_artifact_version_producer_guard ON research_artifact_version;
CREATE OR REPLACE FUNCTION research_artifact_version_producer_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE legacy_attempt research_task_attempt%ROWTYPE; v6_attempt research_work_item_attempt%ROWTYPE;
BEGIN
  IF NEW.produced_by_work_item_attempt_id IS NOT NULL THEN
    SELECT * INTO v6_attempt FROM research_work_item_attempt
      WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id
        AND id=NEW.produced_by_work_item_attempt_id;
    IF NOT FOUND OR v6_attempt.work_item_id IS DISTINCT FROM NEW.produced_by_work_item_id THEN
      RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
    END IF;
    IF NEW.produced_by_agent_id IS NOT NULL
       AND NEW.produced_by_agent_id IS DISTINCT FROM v6_attempt.assigned_agent_id THEN
      RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.produced_by_attempt_id IS NULL OR NEW.produced_by_task_id IS NULL THEN RETURN NEW; END IF;
  SELECT * INTO legacy_attempt FROM research_task_attempt
    WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.produced_by_attempt_id;
  IF NOT FOUND OR legacy_attempt.task_id IS DISTINCT FROM NEW.produced_by_task_id THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
  END IF;
  IF NEW.produced_by_agent_id IS NOT NULL AND NEW.produced_by_agent_id IS DISTINCT FROM legacy_attempt.assigned_agent_id THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
  END IF;
  IF btrim(NEW.model)<>'' AND NEW.model IS DISTINCT FROM legacy_attempt.model THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
  END IF;
  IF btrim(NEW.provider)<>'' AND NEW.provider IS DISTINCT FROM legacy_attempt.provider THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
  END IF;
  IF btrim(NEW.execution_adapter)<>'' AND NEW.execution_adapter IS DISTINCT FROM legacy_attempt.execution_adapter THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_artifact_version_producer_guard';
  END IF;
  RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER research_artifact_version_producer_guard
AFTER INSERT OR UPDATE OF produced_by_task_id,produced_by_attempt_id,produced_by_work_item_id,
  produced_by_work_item_attempt_id,produced_by_agent_id,model,provider,execution_adapter
ON research_artifact_version DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_version_producer_guard_fn();

DROP TRIGGER research_result_attempt_projection_guard ON research_result_artifact;
CREATE OR REPLACE FUNCTION research_result_attempt_projection_guard_fn()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE legacy_attempt research_task_attempt%ROWTYPE; v6_attempt research_work_item_attempt%ROWTYPE;
BEGIN
  IF NEW.work_item_attempt_id IS NOT NULL THEN
    SELECT * INTO v6_attempt FROM research_work_item_attempt
      WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.work_item_attempt_id;
    IF NOT FOUND OR v6_attempt.result_hash IS NULL OR v6_attempt.result_hash IS DISTINCT FROM NEW.content_hash
       OR COALESCE(v6_attempt.client_request_id::text,'') IS DISTINCT FROM NEW.client_request_id
       OR v6_attempt.manifest_id IS DISTINCT FROM NEW.acceptance_work_manifest_id
       OR v6_attempt.manifest_hash IS DISTINCT FROM NEW.acceptance_work_manifest_hash THEN
      RAISE foreign_key_violation USING CONSTRAINT='research_result_attempt_projection_guard';
    END IF;
    RETURN NEW;
  END IF;
  SELECT * INTO legacy_attempt FROM research_task_attempt
    WHERE workspace_id=NEW.workspace_id AND session_id=NEW.session_id AND id=NEW.attempt_id;
  IF NOT FOUND OR legacy_attempt.result_hash IS NULL OR btrim(legacy_attempt.result_hash)=''
     OR NEW.content_hash IS DISTINCT FROM legacy_attempt.result_hash
     OR NEW.client_request_id IS DISTINCT FROM COALESCE(legacy_attempt.client_request_id,'') THEN
    RAISE foreign_key_violation USING CONSTRAINT='research_result_attempt_projection_guard';
  END IF;
  RETURN NEW;
END;
$$;
CREATE CONSTRAINT TRIGGER research_result_attempt_projection_guard
AFTER INSERT OR UPDATE OF workspace_id,session_id,attempt_id,work_item_attempt_id,content_hash,
  client_request_id,acceptance_work_manifest_id,acceptance_work_manifest_hash
ON research_result_artifact DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_result_attempt_projection_guard_fn();

ALTER TABLE research_result_node
  ADD CONSTRAINT research_v6_result_node_artifact_fkey
    FOREIGN KEY (workspace_id,session_id,result_artifact_id)
    REFERENCES research_result_artifact(workspace_id,session_id,id),
  ADD CONSTRAINT research_v6_result_node_version_fkey
    FOREIGN KEY (workspace_id,session_id,artifact_version_id)
    REFERENCES research_artifact_version(workspace_id,session_id,id);
ALTER TABLE research_insight_version ADD CONSTRAINT research_v6_insight_artifact_version_fkey
  FOREIGN KEY (workspace_id,session_id,artifact_version_id)
  REFERENCES research_artifact_version(workspace_id,session_id,id);
ALTER TABLE research_node_branch ADD CONSTRAINT research_v6_node_branch_version_fkey
  FOREIGN KEY (workspace_id,session_id,node_artifact_version_id)
  REFERENCES research_artifact_version(workspace_id,session_id,id);
ALTER TABLE research_branch_frontier ADD CONSTRAINT research_v6_frontier_version_fkey
  FOREIGN KEY (workspace_id,session_id,node_artifact_version_id)
  REFERENCES research_artifact_version(workspace_id,session_id,id);
ALTER TABLE research_node_absorption ADD CONSTRAINT research_v6_absorption_input_version_fkey
  FOREIGN KEY (workspace_id,session_id,input_artifact_version_id)
  REFERENCES research_artifact_version(workspace_id,session_id,id);
ALTER TABLE research_node_steward_assignment ADD CONSTRAINT research_v6_steward_version_fkey
  FOREIGN KEY (workspace_id,session_id,node_artifact_version_id)
  REFERENCES research_artifact_version(workspace_id,session_id,id);
ALTER TABLE research_work_item_attempt ADD CONSTRAINT research_v6_attempt_result_artifact_fkey
  FOREIGN KEY (workspace_id,session_id,result_artifact_id)
  REFERENCES research_result_artifact(workspace_id,session_id,id)
  DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE research_insight_derivation
  ALTER COLUMN input_kind DROP NOT NULL,
  ALTER COLUMN input_entity_id DROP NOT NULL,
  ALTER COLUMN input_content_hash DROP NOT NULL,
  ADD COLUMN insight_version_id UUID,
  ADD COLUMN input_artifact_version_id UUID,
  ADD COLUMN integration_round_id UUID,
  ADD COLUMN input_tier TEXT CHECK(input_tier IN ('S','M','L','XL','XXL')),
  ADD CONSTRAINT research_v6_derivation_shape_check CHECK (
    (insight_version_id IS NULL AND input_artifact_version_id IS NULL AND integration_round_id IS NULL AND input_tier IS NULL
      AND input_kind IS NOT NULL AND input_entity_id IS NOT NULL AND input_content_hash IS NOT NULL)
    OR
    (insight_version_id IS NOT NULL AND input_artifact_version_id IS NOT NULL AND integration_round_id IS NOT NULL AND input_tier IS NOT NULL
      AND input_kind IS NULL AND input_entity_id IS NULL AND input_content_hash IS NULL)
  ),
  ADD CONSTRAINT research_v6_derivation_output_version_fkey
    FOREIGN KEY(workspace_id,session_id,insight_version_id) REFERENCES research_insight_version(workspace_id,session_id,id),
  ADD CONSTRAINT research_v6_derivation_input_version_fkey
    FOREIGN KEY(workspace_id,session_id,input_artifact_version_id) REFERENCES research_artifact_version(workspace_id,session_id,id),
  ADD CONSTRAINT research_v6_derivation_round_fkey
    FOREIGN KEY(workspace_id,session_id,integration_round_id) REFERENCES research_integration_round(workspace_id,session_id,id);
CREATE UNIQUE INDEX research_v6_derivation_exact_idx
  ON research_insight_derivation(session_id,insight_version_id,input_artifact_version_id)
  WHERE insight_version_id IS NOT NULL;
