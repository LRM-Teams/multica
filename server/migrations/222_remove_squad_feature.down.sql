-- Recreate the legacy Squad schema. This rollback restores structure only;
-- rows removed by the up migration cannot be reconstructed.

CREATE TABLE IF NOT EXISTS squad (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    leader_id UUID NOT NULL REFERENCES agent(id) ON DELETE RESTRICT,
    creator_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    archived_at TIMESTAMPTZ,
    archived_by UUID,
    avatar_url TEXT,
    instructions TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_squad_workspace ON squad(workspace_id);

CREATE TABLE IF NOT EXISTS squad_member (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    squad_id UUID NOT NULL REFERENCES squad(id) ON DELETE CASCADE,
    member_type TEXT NOT NULL CHECK (member_type IN ('agent', 'member')),
    member_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(squad_id, member_type, member_id)
);

CREATE INDEX IF NOT EXISTS idx_squad_member_squad ON squad_member(squad_id);
CREATE INDEX IF NOT EXISTS idx_squad_member_entity ON squad_member(member_type, member_id);

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_assignee_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_assignee_type_check
    CHECK (assignee_type IN ('member', 'agent', 'squad'));

ALTER TABLE autopilot DROP CONSTRAINT IF EXISTS autopilot_assignee_type_check;
ALTER TABLE autopilot ADD CONSTRAINT autopilot_assignee_type_check
    CHECK (assignee_type IN ('agent', 'squad'));

ALTER TABLE autopilot_run DROP CONSTRAINT IF EXISTS autopilot_run_squad_id_fkey;
ALTER TABLE autopilot_run
    ADD CONSTRAINT autopilot_run_squad_id_fkey FOREIGN KEY (squad_id) REFERENCES squad(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_autopilot_run_squad_id
    ON autopilot_run (squad_id) WHERE squad_id IS NOT NULL;
