CREATE OR REPLACE FUNCTION research_artifact_entity_kind_allowed(kind TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT kind IN (
    'run_session', 'contract_revision', 'method_decision', 'question', 'task',
    'attempt', 'result_artifact', 'legacy_source', 'source_snapshot',
    'observation', 'claim', 'evidence_link', 'report_revision',
    'evaluation_decision', 'stage_evaluation', 'research_message',
    'product_round_decision', 'context_manifest', 'run_event', 'graph_node',
    'graph_edge', 'hypothesis', 'branch', 'insight', 'inquiry_edge',
    'integration_round', 'integration_contribution', 'insight_derivation',
    'dispute', 'dispute_position', 'deliberation', 'deliberation_turn',
    'search_plan', 'query_execution', 'source_candidate', 'screening_decision'
  );
$$;

CREATE UNIQUE INDEX research_task_attempt_search_lineage_uidx
  ON research_task_attempt (workspace_id, session_id, id, task_id);

CREATE TABLE research_search_plan (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  task_id UUID NOT NULL,
  created_by_attempt_id UUID NOT NULL,
  client_key TEXT NOT NULL CHECK (client_key <> '' AND octet_length(client_key) <= 512),
  objective TEXT NOT NULL CHECK (objective <> '' AND octet_length(objective) <= 32768),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (id),
  UNIQUE (workspace_id, session_id, id),
  UNIQUE (workspace_id, session_id, task_id, client_key),
  FOREIGN KEY (workspace_id, session_id) REFERENCES research_session(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, session_id, task_id) REFERENCES research_task(workspace_id, session_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, session_id, created_by_attempt_id, task_id)
    REFERENCES research_task_attempt(workspace_id, session_id, id, task_id) ON DELETE CASCADE
);

CREATE TABLE research_query_execution (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  search_plan_id UUID NOT NULL,
  client_request_id TEXT NOT NULL CHECK (client_request_id <> '' AND octet_length(client_request_id) <= 512),
  request_hash TEXT NOT NULL CHECK (request_hash ~ '^sha256:[0-9a-f]{64}$'),
  adapter TEXT NOT NULL CHECK (adapter <> '' AND octet_length(adapter) <= 160),
  query_text TEXT NOT NULL CHECK (query_text <> '' AND octet_length(query_text) <= 32768),
  cursor_in TEXT NOT NULL DEFAULT '' CHECK (octet_length(cursor_in) <= 4096),
  cursor_out TEXT NOT NULL DEFAULT '' CHECK (octet_length(cursor_out) <= 4096),
  status TEXT NOT NULL CHECK (status IN ('succeeded', 'failed')),
  failure_class TEXT NOT NULL DEFAULT '' CHECK (octet_length(failure_class) <= 160),
  failure_reason TEXT NOT NULL DEFAULT '' CHECK (octet_length(failure_reason) <= 4096),
  cost JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(cost) = 'object'),
  safety JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(safety) = 'object'),
  executed_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (id),
  UNIQUE (workspace_id, session_id, id),
  UNIQUE (workspace_id, session_id, client_request_id),
  FOREIGN KEY (workspace_id, session_id) REFERENCES research_session(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, session_id, search_plan_id)
    REFERENCES research_search_plan(workspace_id, session_id, id) ON DELETE CASCADE,
  CHECK ((status = 'succeeded' AND failure_class = '' AND failure_reason = '') OR
         (status = 'failed' AND failure_class IN (
           'rate_limited', 'timeout', 'provider_unavailable', 'cursor_expired', 'not_found',
           'permission_denied', 'unsafe_target', 'unsupported_content', 'content_too_large', 'invalid_response'
         ) AND failure_reason <> ''))
);

CREATE TABLE research_source_candidate (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  query_execution_id UUID NOT NULL,
  client_key TEXT NOT NULL CHECK (client_key <> '' AND octet_length(client_key) <= 512),
  canonical_url TEXT NOT NULL CHECK (canonical_url <> '' AND octet_length(canonical_url) <= 8192),
  canonical_identity TEXT NOT NULL CHECK (canonical_identity <> '' AND octet_length(canonical_identity) <= 512),
  title TEXT NOT NULL DEFAULT '' CHECK (octet_length(title) <= 4096),
  snippet TEXT NOT NULL DEFAULT '' CHECK (octet_length(snippet) <= 32768),
  publisher TEXT NOT NULL DEFAULT '' CHECK (octet_length(publisher) <= 4096),
  independence_family TEXT NOT NULL CHECK (independence_family <> '' AND octet_length(independence_family) <= 512),
  content_hash TEXT NOT NULL DEFAULT '' CHECK (content_hash = '' OR content_hash ~ '^sha256:[0-9a-f]{64}$'),
  result_position INTEGER NOT NULL CHECK (result_position > 0),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (id),
  UNIQUE (workspace_id, session_id, id),
  UNIQUE (workspace_id, session_id, query_execution_id, id),
  UNIQUE (workspace_id, session_id, query_execution_id, client_key),
  UNIQUE (workspace_id, session_id, query_execution_id, canonical_identity),
  UNIQUE (workspace_id, session_id, query_execution_id, result_position),
  FOREIGN KEY (workspace_id, session_id) REFERENCES research_session(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, session_id, query_execution_id)
    REFERENCES research_query_execution(workspace_id, session_id, id) ON DELETE CASCADE
);

CREATE TABLE research_screening_decision (
  id UUID NOT NULL DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  query_execution_id UUID NOT NULL,
  source_candidate_id UUID NOT NULL,
  decided_by_attempt_id UUID NOT NULL,
  disposition TEXT NOT NULL CHECK (disposition IN ('accepted', 'excluded', 'duplicate')),
  reason_code TEXT NOT NULL CHECK (reason_code <> '' AND octet_length(reason_code) <= 160),
  reason TEXT NOT NULL CHECK (reason <> '' AND octet_length(reason) <= 4096),
  effective_independence_family TEXT NOT NULL
    CHECK (effective_independence_family <> '' AND octet_length(effective_independence_family) <= 512),
  canonical_candidate_id UUID,
  decided_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (id),
  UNIQUE (workspace_id, session_id, id),
  UNIQUE (workspace_id, session_id, source_candidate_id),
  FOREIGN KEY (workspace_id, session_id) REFERENCES research_session(workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, session_id, query_execution_id)
    REFERENCES research_query_execution(workspace_id, session_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, session_id, query_execution_id, source_candidate_id)
    REFERENCES research_source_candidate(workspace_id, session_id, query_execution_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (workspace_id, session_id, query_execution_id, canonical_candidate_id)
    REFERENCES research_source_candidate(workspace_id, session_id, query_execution_id, id) ON DELETE RESTRICT,
  FOREIGN KEY (workspace_id, session_id, decided_by_attempt_id)
    REFERENCES research_task_attempt(workspace_id, session_id, id) ON DELETE CASCADE,
  CHECK ((disposition = 'duplicate') = (canonical_candidate_id IS NOT NULL)),
  CHECK (canonical_candidate_id IS NULL OR canonical_candidate_id <> source_candidate_id)
);

CREATE CONSTRAINT TRIGGER research_search_plan_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_search_plan
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('search_plan');

CREATE CONSTRAINT TRIGGER research_query_execution_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_query_execution
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('query_execution');

CREATE CONSTRAINT TRIGGER research_source_candidate_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_source_candidate
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('source_candidate');

CREATE CONSTRAINT TRIGGER research_screening_decision_artifact_passport_guard
AFTER INSERT OR UPDATE OF id, workspace_id, session_id ON research_screening_decision
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_artifact_domain_passport_guard_fn('screening_decision');

CREATE OR REPLACE FUNCTION research_search_lineage_passport_delete_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' AND NOT research_artifact_session_still_exists(OLD.workspace_id, OLD.session_id) THEN
    RETURN OLD;
  END IF;
  IF OLD.entity_kind = 'search_plan' AND EXISTS (
    SELECT 1 FROM research_search_plan entity WHERE (entity.workspace_id,entity.session_id,entity.id)=(OLD.workspace_id,OLD.session_id,OLD.id)
  ) OR OLD.entity_kind = 'query_execution' AND EXISTS (
    SELECT 1 FROM research_query_execution entity WHERE (entity.workspace_id,entity.session_id,entity.id)=(OLD.workspace_id,OLD.session_id,OLD.id)
  ) OR OLD.entity_kind = 'source_candidate' AND EXISTS (
    SELECT 1 FROM research_source_candidate entity WHERE (entity.workspace_id,entity.session_id,entity.id)=(OLD.workspace_id,OLD.session_id,OLD.id)
  ) OR OLD.entity_kind = 'screening_decision' AND EXISTS (
    SELECT 1 FROM research_screening_decision entity WHERE (entity.workspace_id,entity.session_id,entity.id)=(OLD.workspace_id,OLD.session_id,OLD.id)
  ) THEN
    RAISE EXCEPTION 'Research Search/Corpus passport cannot be deleted while domain row exists'
      USING ERRCODE = '55000', CONSTRAINT = 'research_search_lineage_passport_delete_guard';
  END IF;
  RETURN OLD;
END;
$$;

CREATE TRIGGER research_search_lineage_passport_delete_guard
BEFORE DELETE ON research_artifact_passport
FOR EACH ROW EXECUTE FUNCTION research_search_lineage_passport_delete_guard_fn();

ALTER TABLE research_source_snapshot
  ADD COLUMN ingestion_kind TEXT NOT NULL DEFAULT 'agent_direct_evidence'
    CHECK (ingestion_kind IN ('agent_direct_evidence', 'screened_retrieval')),
  ADD COLUMN screening_decision_id UUID,
  ADD CONSTRAINT research_source_snapshot_screening_decision_scoped_fkey
    FOREIGN KEY (workspace_id, session_id, screening_decision_id)
    REFERENCES research_screening_decision(workspace_id, session_id, id) ON DELETE RESTRICT,
  ADD CONSTRAINT research_source_snapshot_ingestion_lineage_check
    CHECK ((ingestion_kind = 'agent_direct_evidence' AND screening_decision_id IS NULL) OR
           (ingestion_kind = 'screened_retrieval' AND screening_decision_id IS NOT NULL));

CREATE OR REPLACE FUNCTION research_validate_source_snapshot_screening_lineage()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  candidate research_source_candidate%ROWTYPE;
BEGIN
  IF NEW.ingestion_kind = 'agent_direct_evidence' THEN
    RETURN NEW;
  END IF;

  SELECT c.* INTO candidate
  FROM research_screening_decision d
  JOIN research_source_candidate c
    ON (c.workspace_id, c.session_id, c.id) =
       (d.workspace_id, d.session_id, d.source_candidate_id)
  WHERE d.workspace_id = NEW.workspace_id
    AND d.session_id = NEW.session_id
    AND d.id = NEW.screening_decision_id
    AND d.disposition = 'accepted';

  IF NOT FOUND OR candidate.canonical_url <> NEW.canonical_url OR
     (candidate.content_hash <> '' AND candidate.content_hash <> ('sha256:' || NEW.content_hash)) THEN
    RAISE EXCEPTION 'screened Research source must match an accepted Screening Decision'
      USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END;
$$;

CREATE CONSTRAINT TRIGGER research_source_snapshot_screening_lineage_guard
AFTER INSERT OR UPDATE OF workspace_id, session_id, canonical_url, content_hash, ingestion_kind, screening_decision_id
ON research_source_snapshot
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION research_validate_source_snapshot_screening_lineage();

CREATE OR REPLACE FUNCTION research_reject_search_lineage_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF TG_OP = 'DELETE' AND NOT research_artifact_session_still_exists(OLD.workspace_id, OLD.session_id) THEN
    RETURN OLD;
  END IF;
  RAISE EXCEPTION 'Research Search/Corpus lineage is append-only'
    USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER research_query_execution_append_only_guard
BEFORE UPDATE OR DELETE ON research_query_execution
FOR EACH ROW EXECUTE FUNCTION research_reject_search_lineage_mutation();

CREATE TRIGGER research_search_plan_append_only_guard
BEFORE UPDATE OR DELETE ON research_search_plan
FOR EACH ROW EXECUTE FUNCTION research_reject_search_lineage_mutation();

CREATE TRIGGER research_source_candidate_append_only_guard
BEFORE UPDATE OR DELETE ON research_source_candidate
FOR EACH ROW EXECUTE FUNCTION research_reject_search_lineage_mutation();

CREATE TRIGGER research_screening_decision_append_only_guard
BEFORE UPDATE OR DELETE ON research_screening_decision
FOR EACH ROW EXECUTE FUNCTION research_reject_search_lineage_mutation();

CREATE INDEX research_query_execution_plan_idx
  ON research_query_execution (workspace_id, session_id, search_plan_id, executed_at, id);
CREATE INDEX research_source_candidate_query_idx
  ON research_source_candidate (workspace_id, session_id, query_execution_id, result_position, id);
CREATE INDEX research_screening_decision_disposition_idx
  ON research_screening_decision (workspace_id, session_id, disposition, decided_at, id);
