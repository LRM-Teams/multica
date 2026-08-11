-- Roll back Chapter D1i scoped relationship FKs.

ALTER TABLE research_source DROP CONSTRAINT IF EXISTS research_source_source_snapshot_scoped_fkey;
ALTER TABLE research_source
  ADD CONSTRAINT research_source_source_snapshot_id_fkey
  FOREIGN KEY (source_snapshot_id) REFERENCES research_source_snapshot(id) ON DELETE SET NULL;

ALTER TABLE research_claim_evidence DROP CONSTRAINT IF EXISTS research_claim_evidence_verified_by_task_scoped_fkey;
ALTER TABLE research_claim_evidence
  ADD CONSTRAINT research_claim_evidence_verified_by_task_id_fkey
  FOREIGN KEY (verified_by_task_id) REFERENCES research_task(id) ON DELETE SET NULL;

ALTER TABLE research_claim_evidence DROP CONSTRAINT IF EXISTS research_claim_evidence_observation_scoped_fkey;
ALTER TABLE research_claim_evidence
  ADD CONSTRAINT research_claim_evidence_observation_id_fkey
  FOREIGN KEY (observation_id) REFERENCES research_observation(id) ON DELETE CASCADE;

ALTER TABLE research_claim_evidence DROP CONSTRAINT IF EXISTS research_claim_evidence_claim_scoped_fkey;
ALTER TABLE research_claim_evidence
  ADD CONSTRAINT research_claim_evidence_claim_id_fkey
  FOREIGN KEY (claim_id) REFERENCES research_claim(id) ON DELETE CASCADE;

ALTER TABLE research_claim DROP CONSTRAINT IF EXISTS research_claim_produced_by_task_scoped_fkey;
ALTER TABLE research_claim
  ADD CONSTRAINT research_claim_produced_by_task_id_fkey
  FOREIGN KEY (produced_by_task_id) REFERENCES research_task(id) ON DELETE SET NULL;

ALTER TABLE research_observation DROP CONSTRAINT IF EXISTS research_observation_produced_by_task_scoped_fkey;
ALTER TABLE research_observation
  ADD CONSTRAINT research_observation_produced_by_task_id_fkey
  FOREIGN KEY (produced_by_task_id) REFERENCES research_task(id) ON DELETE SET NULL;

ALTER TABLE research_observation DROP CONSTRAINT IF EXISTS research_observation_source_snapshot_scoped_fkey;
ALTER TABLE research_observation
  ADD CONSTRAINT research_observation_source_snapshot_id_fkey
  FOREIGN KEY (source_snapshot_id) REFERENCES research_source_snapshot(id) ON DELETE CASCADE;

ALTER TABLE research_source_snapshot DROP CONSTRAINT IF EXISTS research_source_snapshot_produced_by_task_scoped_fkey;
ALTER TABLE research_source_snapshot
  ADD CONSTRAINT research_source_snapshot_produced_by_task_id_fkey
  FOREIGN KEY (produced_by_task_id) REFERENCES research_task(id) ON DELETE SET NULL;

ALTER TABLE research_question DROP CONSTRAINT IF EXISTS research_question_answer_claim_scoped_fkey;
ALTER TABLE research_question
  ADD CONSTRAINT research_question_answer_claim_fk
  FOREIGN KEY (answer_claim_id) REFERENCES research_claim(id) ON DELETE SET NULL;

ALTER TABLE research_question DROP CONSTRAINT IF EXISTS research_question_created_by_task_scoped_fkey;
ALTER TABLE research_question
  ADD CONSTRAINT research_question_created_by_task_fk
  FOREIGN KEY (created_by_task_id) REFERENCES research_task(id) ON DELETE SET NULL;

ALTER TABLE research_question DROP CONSTRAINT IF EXISTS research_question_parent_question_scoped_fkey;
ALTER TABLE research_question
  ADD CONSTRAINT research_question_parent_question_id_fkey
  FOREIGN KEY (parent_question_id) REFERENCES research_question(id) ON DELETE SET NULL;

ALTER TABLE research_task DROP CONSTRAINT IF EXISTS research_task_parent_task_scoped_fkey;
ALTER TABLE research_task
  ADD CONSTRAINT research_task_parent_task_id_fkey
  FOREIGN KEY (parent_task_id) REFERENCES research_task(id) ON DELETE SET NULL;

ALTER TABLE research_task DROP CONSTRAINT IF EXISTS research_task_question_scoped_fkey;
ALTER TABLE research_task
  ADD CONSTRAINT research_task_question_id_fkey
  FOREIGN KEY (question_id) REFERENCES research_question(id) ON DELETE SET NULL;

ALTER TABLE research_task_attempt DROP CONSTRAINT IF EXISTS research_task_attempt_task_scoped_fkey;
ALTER TABLE research_task_attempt
  ADD CONSTRAINT research_task_attempt_task_id_fkey
  FOREIGN KEY (task_id) REFERENCES research_task(id) ON DELETE CASCADE;

ALTER TABLE research_task_dependency DROP CONSTRAINT IF EXISTS research_task_dependency_depends_on_scoped_fkey;
ALTER TABLE research_task_dependency DROP CONSTRAINT IF EXISTS research_task_dependency_task_scoped_fkey;
ALTER TABLE research_task_dependency DROP CONSTRAINT IF EXISTS research_task_dependency_session_fkey;
ALTER TABLE research_task_dependency DROP CONSTRAINT IF EXISTS research_task_dependency_pkey;
ALTER TABLE research_task_dependency
  ADD CONSTRAINT research_task_dependency_pkey PRIMARY KEY (task_id, depends_on_task_id);
ALTER TABLE research_task_dependency
  ADD CONSTRAINT research_task_dependency_task_id_fkey
  FOREIGN KEY (task_id) REFERENCES research_task(id) ON DELETE CASCADE;
ALTER TABLE research_task_dependency
  ADD CONSTRAINT research_task_dependency_depends_on_task_id_fkey
  FOREIGN KEY (depends_on_task_id) REFERENCES research_task(id) ON DELETE CASCADE;
ALTER TABLE research_task_dependency
  DROP COLUMN IF EXISTS workspace_id,
  DROP COLUMN IF EXISTS session_id;
