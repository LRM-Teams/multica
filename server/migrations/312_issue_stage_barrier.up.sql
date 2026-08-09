-- Agent Coordination Protocol v1: IssueStageBarrier.
-- Child issue completion must not wake the parent on every child. The parent
-- is notified once when the current stage barrier closes.

CREATE TABLE issue_stage (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    parent_issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    stage_key TEXT NOT NULL DEFAULT 'implicit',
    status TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open', 'closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (parent_issue_id, stage_key)
);

CREATE INDEX idx_issue_stage_parent_status
    ON issue_stage(parent_issue_id, status);

CREATE TABLE issue_stage_child (
    stage_id UUID NOT NULL REFERENCES issue_stage(id) ON DELETE CASCADE,
    child_issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (stage_id, child_issue_id),
    UNIQUE (child_issue_id)
);

CREATE INDEX idx_issue_stage_child_stage
    ON issue_stage_child(stage_id);

CREATE TABLE issue_stage_barrier_event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    parent_issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    stage_id UUID NOT NULL REFERENCES issue_stage(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('closed')),
    trigger_child_issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (stage_id, event_type)
);

CREATE INDEX idx_issue_stage_barrier_event_parent
    ON issue_stage_barrier_event(parent_issue_id, created_at DESC);
