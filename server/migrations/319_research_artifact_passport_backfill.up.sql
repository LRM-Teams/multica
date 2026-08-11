-- Chapter D1b: honest legacy artifact passport backfill for initialized Run entities.

CREATE OR REPLACE FUNCTION research_artifact_migration_content_hash(
  kind TEXT,
  workspace_id UUID,
  session_id UUID,
  entity_id UUID
)
RETURNS TEXT
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT 'sha256:' || encode(digest(
    convert_to(
      'research-artifact-migration:' || kind || ':' || workspace_id::text || ':' || session_id::text || ':' || entity_id::text,
      'UTF8'
    ),
    'sha256'
  ), 'hex');
$$;

CREATE OR REPLACE FUNCTION research_artifact_backfill_registered(
  p_workspace_id UUID,
  p_session_id UUID,
  p_entity_id UUID,
  p_kind TEXT,
  p_source_created_at TIMESTAMPTZ,
  p_goal_version INTEGER DEFAULT NULL,
  p_plan_version INTEGER DEFAULT NULL
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
  v_hash TEXT;
BEGIN
  IF NOT research_artifact_entity_kind_allowed(p_kind) THEN
    RAISE EXCEPTION 'unknown artifact kind %', p_kind;
  END IF;
  v_hash := research_artifact_migration_content_hash(
    p_kind, p_workspace_id, p_session_id, p_entity_id
  );
  INSERT INTO research_artifact_passport (
    id, workspace_id, session_id, entity_kind, current_version, eligibility_revision,
    lifecycle_status, provenance_completeness, source_created_at, registered_at
  ) VALUES (
    p_entity_id, p_workspace_id, p_session_id, p_kind, NULL, 1,
    'registered', 'partial', p_source_created_at, now()
  )
  ON CONFLICT (workspace_id, session_id, id) DO NOTHING;

  INSERT INTO research_artifact_version (
    workspace_id, session_id, artifact_id, version, schema_name, schema_version,
    canonicalization_version, content_hash, access_level, goal_version, plan_version,
    hash_origin
  )
  SELECT
    p_workspace_id, p_session_id, p_entity_id, 1, p_kind, 'legacy-v1',
    'research-artifact-c14n-v1', v_hash, 'raw', p_goal_version, p_plan_version,
    'migration_recomputed'
  WHERE NOT EXISTS (
    SELECT 1 FROM research_artifact_version existing
    WHERE existing.workspace_id = p_workspace_id
      AND existing.session_id = p_session_id
      AND existing.artifact_id = p_entity_id
      AND existing.version = 1
  );

  UPDATE research_artifact_passport
  SET current_version = 1
  WHERE workspace_id = p_workspace_id
    AND session_id = p_session_id
    AND id = p_entity_id
    AND current_version IS NULL;
END;
$$;

SELECT research_artifact_backfill_registered(
  r.workspace_id, r.session_id, r.id, 'contract_revision', r.created_at, r.goal_version, NULL
)
FROM research_contract_revision r;

SELECT research_artifact_backfill_registered(
  q.workspace_id, q.session_id, q.id, 'question', q.created_at, q.goal_version, q.plan_version
)
FROM research_question q;

SELECT research_artifact_backfill_registered(
  t.workspace_id, t.session_id, t.id, 'task', t.created_at, t.goal_version, t.plan_version
)
FROM research_task t;

SELECT research_artifact_backfill_registered(
  a.workspace_id, a.session_id, a.id, 'attempt', a.created_at, NULL, NULL
)
FROM research_task_attempt a;

SELECT research_artifact_backfill_registered(
  c.workspace_id, c.session_id, c.id, 'claim', c.created_at, c.goal_version, c.plan_version
)
FROM research_claim c;

SELECT research_artifact_backfill_registered(
  o.workspace_id, o.session_id, o.id, 'observation', o.created_at, NULL, NULL
)
FROM research_observation o;

SELECT research_artifact_backfill_registered(
  s.workspace_id, s.session_id, s.id, 'source_snapshot', s.created_at, NULL, NULL
)
FROM research_source_snapshot s;

SELECT research_artifact_backfill_registered(
  e.workspace_id, e.session_id, e.id, 'evidence_link', e.created_at, NULL, NULL
)
FROM research_claim_evidence e;

SELECT research_artifact_backfill_registered(
  d.workspace_id, d.session_id, d.id,
  CASE WHEN d.decision_kind = 'research_method' THEN 'method_decision' ELSE 'evaluation_decision' END,
  d.created_at, d.goal_version, d.plan_version
)
FROM research_decision d;

SELECT research_artifact_backfill_registered(
  r.workspace_id, r.session_id, r.id, 'report_revision', r.created_at, r.goal_version, r.plan_version
)
FROM research_report r;

SELECT research_artifact_backfill_registered(
  e.workspace_id, e.session_id, e.id, 'stage_evaluation', e.created_at, NULL, NULL
)
FROM research_stage_eval e;

SELECT research_artifact_backfill_registered(
  m.workspace_id, m.session_id, m.id, 'research_message', m.created_at, NULL, NULL
)
FROM research_message m;

SELECT research_artifact_backfill_registered(
  p.workspace_id, p.session_id, p.id, 'product_round_decision', p.created_at, NULL, NULL
)
FROM research_product_round_card p;

SELECT research_artifact_backfill_registered(
  s.workspace_id, s.session_id, s.id, 'legacy_source', s.created_at, NULL, NULL
)
FROM research_source s;

SELECT research_artifact_backfill_registered(
  n.workspace_id, n.session_id, n.id, 'graph_node', n.created_at, NULL, NULL
)
FROM research_graph_node n;

SELECT research_artifact_backfill_registered(
  e.workspace_id, e.session_id, e.id, 'graph_edge', e.created_at, NULL, NULL
)
FROM research_graph_edge e;

SELECT research_artifact_backfill_registered(
  e.workspace_id, e.session_id, e.id, 'run_event', e.created_at, NULL, NULL
)
FROM research_run_event e;

-- Upgrade run_session passports created by 318 without versions.
SELECT research_artifact_backfill_registered(
  p.workspace_id, p.session_id, p.id, 'run_session', COALESCE(p.source_created_at, s.created_at), NULL, NULL
)
FROM research_artifact_passport p
JOIN research_session s ON s.id = p.id AND s.workspace_id = p.workspace_id
WHERE p.entity_kind = 'run_session' AND p.current_version IS NULL;
