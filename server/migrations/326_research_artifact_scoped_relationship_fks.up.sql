-- Chapter D1i: scoped composite FKs for session-owned Research relationships (design §4.8).

ALTER TABLE research_task_dependency
  ADD COLUMN IF NOT EXISTS workspace_id UUID,
  ADD COLUMN IF NOT EXISTS session_id UUID;

UPDATE research_task_dependency d
SET workspace_id = t.workspace_id,
    session_id = t.session_id
FROM research_task t
WHERE t.id = d.task_id
  AND (d.workspace_id IS NULL OR d.session_id IS NULL);

ALTER TABLE research_task_dependency
  ALTER COLUMN workspace_id SET NOT NULL,
  ALTER COLUMN session_id SET NOT NULL;

ALTER TABLE research_task_dependency DROP CONSTRAINT IF EXISTS research_task_dependency_pkey;
ALTER TABLE research_task_dependency DROP CONSTRAINT IF EXISTS research_task_dependency_task_id_fkey;
ALTER TABLE research_task_dependency DROP CONSTRAINT IF EXISTS research_task_dependency_depends_on_task_id_fkey;

ALTER TABLE research_task_dependency
  ADD CONSTRAINT research_task_dependency_pkey
  PRIMARY KEY (workspace_id, session_id, task_id, depends_on_task_id);

ALTER TABLE research_task_dependency
  ADD CONSTRAINT research_task_dependency_session_fkey
  FOREIGN KEY (workspace_id, session_id)
  REFERENCES research_session (workspace_id, id) ON DELETE CASCADE;

ALTER TABLE research_task_dependency
  ADD CONSTRAINT research_task_dependency_task_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, task_id)
  REFERENCES research_task (workspace_id, session_id, id) ON DELETE CASCADE;

ALTER TABLE research_task_dependency
  ADD CONSTRAINT research_task_dependency_depends_on_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, depends_on_task_id)
  REFERENCES research_task (workspace_id, session_id, id) ON DELETE CASCADE;

ALTER TABLE research_task_attempt DROP CONSTRAINT IF EXISTS research_task_attempt_task_id_fkey;
ALTER TABLE research_task_attempt
  ADD CONSTRAINT research_task_attempt_task_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, task_id)
  REFERENCES research_task (workspace_id, session_id, id) ON DELETE CASCADE;

ALTER TABLE research_task DROP CONSTRAINT IF EXISTS research_task_question_id_fkey;
ALTER TABLE research_task
  ADD CONSTRAINT research_task_question_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, question_id)
  REFERENCES research_question (workspace_id, session_id, id) ON DELETE SET NULL;

ALTER TABLE research_task DROP CONSTRAINT IF EXISTS research_task_parent_task_id_fkey;
ALTER TABLE research_task
  ADD CONSTRAINT research_task_parent_task_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, parent_task_id)
  REFERENCES research_task (workspace_id, session_id, id) ON DELETE SET NULL;

ALTER TABLE research_question DROP CONSTRAINT IF EXISTS research_question_parent_question_id_fkey;
ALTER TABLE research_question
  ADD CONSTRAINT research_question_parent_question_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, parent_question_id)
  REFERENCES research_question (workspace_id, session_id, id) ON DELETE SET NULL;

ALTER TABLE research_question DROP CONSTRAINT IF EXISTS research_question_created_by_task_fk;
ALTER TABLE research_question
  ADD CONSTRAINT research_question_created_by_task_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, created_by_task_id)
  REFERENCES research_task (workspace_id, session_id, id) ON DELETE SET NULL;

ALTER TABLE research_question DROP CONSTRAINT IF EXISTS research_question_answer_claim_fk;
ALTER TABLE research_question
  ADD CONSTRAINT research_question_answer_claim_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, answer_claim_id)
  REFERENCES research_claim (workspace_id, session_id, id) ON DELETE SET NULL;

ALTER TABLE research_source_snapshot DROP CONSTRAINT IF EXISTS research_source_snapshot_produced_by_task_id_fkey;
ALTER TABLE research_source_snapshot
  ADD CONSTRAINT research_source_snapshot_produced_by_task_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, produced_by_task_id)
  REFERENCES research_task (workspace_id, session_id, id) ON DELETE SET NULL;

ALTER TABLE research_observation DROP CONSTRAINT IF EXISTS research_observation_source_snapshot_id_fkey;
ALTER TABLE research_observation
  ADD CONSTRAINT research_observation_source_snapshot_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, source_snapshot_id)
  REFERENCES research_source_snapshot (workspace_id, session_id, id) ON DELETE CASCADE;

ALTER TABLE research_observation DROP CONSTRAINT IF EXISTS research_observation_produced_by_task_id_fkey;
ALTER TABLE research_observation
  ADD CONSTRAINT research_observation_produced_by_task_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, produced_by_task_id)
  REFERENCES research_task (workspace_id, session_id, id) ON DELETE SET NULL;

ALTER TABLE research_claim DROP CONSTRAINT IF EXISTS research_claim_produced_by_task_id_fkey;
ALTER TABLE research_claim
  ADD CONSTRAINT research_claim_produced_by_task_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, produced_by_task_id)
  REFERENCES research_task (workspace_id, session_id, id) ON DELETE SET NULL;

ALTER TABLE research_claim_evidence DROP CONSTRAINT IF EXISTS research_claim_evidence_claim_id_fkey;
ALTER TABLE research_claim_evidence
  ADD CONSTRAINT research_claim_evidence_claim_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, claim_id)
  REFERENCES research_claim (workspace_id, session_id, id) ON DELETE CASCADE;

ALTER TABLE research_claim_evidence DROP CONSTRAINT IF EXISTS research_claim_evidence_observation_id_fkey;
ALTER TABLE research_claim_evidence
  ADD CONSTRAINT research_claim_evidence_observation_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, observation_id)
  REFERENCES research_observation (workspace_id, session_id, id) ON DELETE CASCADE;

ALTER TABLE research_claim_evidence DROP CONSTRAINT IF EXISTS research_claim_evidence_verified_by_task_id_fkey;
ALTER TABLE research_claim_evidence
  ADD CONSTRAINT research_claim_evidence_verified_by_task_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, verified_by_task_id)
  REFERENCES research_task (workspace_id, session_id, id) ON DELETE SET NULL;

ALTER TABLE research_source DROP CONSTRAINT IF EXISTS research_source_source_snapshot_id_fkey;
ALTER TABLE research_source
  ADD CONSTRAINT research_source_source_snapshot_scoped_fkey
  FOREIGN KEY (workspace_id, session_id, source_snapshot_id)
  REFERENCES research_source_snapshot (workspace_id, session_id, id) ON DELETE SET NULL;
