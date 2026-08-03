CREATE TABLE source_task (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  type text NOT NULL CHECK (type IN ('issue', 'message')),
  payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
  content_hash text NOT NULL CHECK (length(content_hash) = 64),
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, content_hash)
);

ALTER TABLE env_dispatch_run
  ADD COLUMN run_id uuid NOT NULL DEFAULT gen_random_uuid(),
  ADD COLUMN source_task_id uuid REFERENCES source_task(id) ON DELETE RESTRICT,
  ADD COLUMN sample_index integer NOT NULL DEFAULT 0,
  ADD COLUMN local_issue_id uuid REFERENCES issue(id) ON DELETE SET NULL,
  ADD COLUMN local_channel_id uuid REFERENCES channel(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX env_dispatch_run_run_id_uidx ON env_dispatch_run (run_id);
