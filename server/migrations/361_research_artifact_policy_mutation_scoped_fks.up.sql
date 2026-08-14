-- Chapter D §15.3: make policy-ledger target scope structural, in addition to
-- the semantic deferred reciprocity guards.

ALTER TABLE research_artifact_policy_mutation
  ADD CONSTRAINT research_artifact_policy_mutation_artifact_scoped_fkey
    FOREIGN KEY (workspace_id, session_id, artifact_id)
    REFERENCES research_artifact_passport (workspace_id, session_id, id)
    NOT VALID,
  ADD CONSTRAINT research_artifact_policy_mutation_grant_scoped_fkey
    FOREIGN KEY (workspace_id, session_id, policy_grant_id)
    REFERENCES research_artifact_policy_grant (workspace_id, session_id, id)
    NOT VALID;

ALTER TABLE research_artifact_policy_mutation
  VALIDATE CONSTRAINT research_artifact_policy_mutation_artifact_scoped_fkey;

ALTER TABLE research_artifact_policy_mutation
  VALIDATE CONSTRAINT research_artifact_policy_mutation_grant_scoped_fkey;
