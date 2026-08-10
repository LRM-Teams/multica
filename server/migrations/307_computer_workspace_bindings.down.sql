-- 307: Computer Workspace Execution Bindings (backward)
--
-- Deployment (backward): drops only the Computer connection and owner tables.
-- Safe on any replica before or after a forward; re-created on the next
-- forward.

DROP TABLE IF EXISTS computer_workspace_bindings;
DROP TABLE IF EXISTS computer_identity_owner;
