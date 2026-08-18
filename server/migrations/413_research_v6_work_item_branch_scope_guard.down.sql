ALTER TABLE research_v6_work_item_branch
  DROP CONSTRAINT IF EXISTS research_v6_work_item_branch_work_item_scope_fkey,
  ADD CONSTRAINT research_v6_work_item_branch_work_item_id_fkey
    FOREIGN KEY (work_item_id) REFERENCES research_work_item(id) ON DELETE CASCADE;
ALTER TABLE research_work_item
  DROP CONSTRAINT IF EXISTS research_v6_work_item_workspace_session_id_key;
