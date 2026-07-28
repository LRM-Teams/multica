-- DATA LOSS WARNING: this rollback discards every agent workspace-role
-- assignment, including admin delegations. Before applying it, export or
-- reconcile any admin assignments that must survive; this is not a lossless
-- application rollback.
ALTER TABLE agent
  DROP CONSTRAINT agent_workspace_role_check;

ALTER TABLE agent
  DROP COLUMN workspace_role;
