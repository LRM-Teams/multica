-- Agent Coordination Protocol v1: WorkOwnerLease.
-- One active executor lease per issue prevents dual-agent / dual-branch split brain.
-- Reviewer leases may be multiple; they cannot claim executor ownership.

CREATE TABLE work_owner_lease (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
    owner_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'executor'
        CHECK (role IN ('executor', 'reviewer', 'coordinator')),
    canonical_branch TEXT,
    conversation_id TEXT,
    runtime_lane TEXT,
    allowed_paths JSONB NOT NULL DEFAULT '[]'::jsonb,
    migration_numbers JSONB NOT NULL DEFAULT '[]'::jsonb,
    handoff_to UUID REFERENCES agent(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'released', 'expired', 'handed_off')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Only one active executor lease per issue.
CREATE UNIQUE INDEX uq_work_owner_lease_issue_executor_active
    ON work_owner_lease(issue_id)
    WHERE status = 'active' AND role = 'executor';

CREATE INDEX idx_work_owner_lease_owner_status
    ON work_owner_lease(owner_agent_id, status, expires_at);

CREATE INDEX idx_work_owner_lease_workspace_issue
    ON work_owner_lease(workspace_id, issue_id, status);

CREATE INDEX idx_work_owner_lease_owner_agent_id
    ON work_owner_lease(owner_agent_id);

CREATE INDEX idx_work_owner_lease_handoff_to
    ON work_owner_lease(handoff_to)
    WHERE handoff_to IS NOT NULL;
