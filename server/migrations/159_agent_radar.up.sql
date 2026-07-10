CREATE TABLE agent_radar_run (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
    trigger_kind TEXT NOT NULL CHECK (trigger_kind IN ('scheduled', 'event', 'manual')),
    trigger_ref TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned', 'queued', 'running', 'succeeded', 'no_action', 'failed', 'cancelled')),
    cooldown_key TEXT NOT NULL DEFAULT '',
    context_summary TEXT NOT NULL DEFAULT '',
    action_plan JSONB NOT NULL DEFAULT '{}'::jsonb,
    error TEXT NOT NULL DEFAULT '',
    scheduled_for TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_radar_run_workspace_agent_created
    ON agent_radar_run (workspace_id, agent_id, created_at DESC, id DESC);

CREATE INDEX idx_agent_radar_run_planned
    ON agent_radar_run (scheduled_for, created_at)
    WHERE status = 'planned';

CREATE INDEX idx_agent_radar_run_agent_cooldown
    ON agent_radar_run (workspace_id, agent_id, cooldown_key, created_at DESC)
    WHERE status IN ('planned', 'queued', 'running', 'succeeded', 'no_action');

CREATE TABLE agent_radar_action (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    radar_run_id UUID NOT NULL REFERENCES agent_radar_run(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    action_type TEXT NOT NULL CHECK (action_type IN ('no_action', 'post_channel_message', 'reply_thread', 'mention_agent', 'create_issue', 'comment_issue', 'assign_issue', 'schedule_reminder', 'update_agent_plan')),
    status TEXT NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed', 'approved', 'executing', 'executed', 'blocked', 'failed', 'skipped')),
    risk_level TEXT NOT NULL DEFAULT 'low' CHECK (risk_level IN ('low', 'medium', 'high')),
    confidence TEXT NOT NULL DEFAULT 'medium' CHECK (confidence IN ('low', 'medium', 'high')),
    dedupe_key TEXT NOT NULL DEFAULT '',
    target_kind TEXT NOT NULL DEFAULT 'none' CHECK (target_kind IN ('none', 'channel', 'thread', 'issue', 'agent', 'reminder', 'plan')),
    target_id UUID,
    reason TEXT NOT NULL DEFAULT '',
    evidence JSONB NOT NULL DEFAULT '[]'::jsonb,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_agent_radar_action_run
    ON agent_radar_action (radar_run_id, created_at ASC, id ASC);

CREATE INDEX idx_agent_radar_action_workspace_agent_created
    ON agent_radar_action (workspace_id, agent_id, created_at DESC, id DESC);

CREATE UNIQUE INDEX idx_agent_radar_action_dedupe_active
    ON agent_radar_action (workspace_id, agent_id, dedupe_key)
    WHERE dedupe_key <> '' AND status IN ('proposed', 'approved', 'executing', 'executed');
