-- 158_interaction_dag_session_run: maps an areal RL session_id to the multica
-- agent_run_id (= task.ID, D8) and the issue_id known at session-open. U9
-- omitted this table; CloseSegmentForEvent looks up agent_run_id + issue_id by
-- session_id here, and U8 assembles session_to_agent_run from it.
CREATE TABLE IF NOT EXISTS interaction_dag_session_run (
    session_id text PRIMARY KEY,
    project_id text NOT NULL,
    agent_run_id text NOT NULL,
    issue_id text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_interaction_dag_session_run_project
    ON interaction_dag_session_run (project_id);
