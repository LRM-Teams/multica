-- Durable dispatch root for env-dispatch projects (spec: durable dispatch
-- identity independent of training_dispatch). One row per project, keyed by
-- project_id, carrying the workspace, the requested training_mode, and the
-- nullable root_task_id bound to the enqueued leader task. GET /dag derives
-- readiness exclusively from this row (root_task_id -> agent_task_queue.status)
-- so a non-training dispatch returns a complete DAG without any training_dispatch
-- row. root_task_id is nullable + ON DELETE SET NULL so a deleted task leaves the
-- run row intact (the /dag endpoint treats an unbound root as in_progress).
CREATE TABLE env_dispatch_run (
  project_id   uuid PRIMARY KEY REFERENCES project(id) ON DELETE CASCADE,
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  training_mode boolean NOT NULL,
  root_task_id  uuid REFERENCES agent_task_queue(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);
