-- Agent Coordination Protocol v1: MigrationLease.
-- Agents must reserve a migration number before adding a new *.up.sql file.
-- Duplicate reserved/open numbers fail closed so PRs cannot collide on the same
-- number the way #2567 vs #2568 did on 308.

CREATE TABLE migration_lease (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    migration_number INTEGER NOT NULL CHECK (migration_number > 0),
    owner_agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    issue_id UUID REFERENCES issue(id) ON DELETE SET NULL,
    pr_number INTEGER,
    filename TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'reserved'
        CHECK (status IN ('reserved', 'committed', 'released', 'expired')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most one active (reserved) lease per migration number globally.
CREATE UNIQUE INDEX uq_migration_lease_number_active
    ON migration_lease(migration_number)
    WHERE status = 'reserved';

CREATE INDEX idx_migration_lease_owner_status
    ON migration_lease(owner_agent_id, status, expires_at);

CREATE INDEX idx_migration_lease_workspace_status
    ON migration_lease(workspace_id, status, migration_number);

-- Agent delete FK index convention.
CREATE INDEX idx_migration_lease_owner_agent_id
    ON migration_lease(owner_agent_id);
