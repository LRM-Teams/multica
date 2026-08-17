ALTER TABLE research_work_item
  ADD CONSTRAINT research_v6_work_item_workspace_session_id_key UNIQUE (workspace_id, session_id, id);
ALTER TABLE research_v6_work_item_branch
  DROP CONSTRAINT IF EXISTS research_v6_work_item_branch_work_item_id_fkey,
  ADD CONSTRAINT research_v6_work_item_branch_work_item_scope_fkey
    FOREIGN KEY (workspace_id, session_id, work_item_id)
    REFERENCES research_work_item (workspace_id, session_id, id) ON DELETE CASCADE;
