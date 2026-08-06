ALTER TABLE agent_activity_event
    ADD COLUMN IF NOT EXISTS action_label TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reason_label TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tone TEXT NOT NULL DEFAULT 'action'
        CHECK (tone IN ('wake', 'action', 'progress', 'success', 'failure', 'muted'));

ALTER TABLE task_message
    ADD COLUMN IF NOT EXISTS action_label TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS summary TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS tone TEXT NOT NULL DEFAULT 'action'
        CHECK (tone IN ('wake', 'action', 'progress', 'success', 'failure', 'muted'));
