-- 307: Computer Workspace Execution Bindings (backward)
--
-- Deployment (backward): drops only this table. Safe on any replica before or
-- after a forward; re-created on the next forward.

DROP TABLE IF EXISTS computer_workspace_bindings;
