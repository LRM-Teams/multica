CREATE TABLE research_v6_work_item_branch (
  workspace_id UUID NOT NULL,
  session_id UUID NOT NULL,
  work_item_id UUID NOT NULL,
  branch_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, session_id, work_item_id, branch_id),
  FOREIGN KEY (work_item_id) REFERENCES research_work_item(id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, session_id, branch_id) REFERENCES research_branch(workspace_id, session_id, id) ON DELETE CASCADE
);
CREATE INDEX research_v6_work_item_branch_branch_idx ON research_v6_work_item_branch(session_id, branch_id, work_item_id);
