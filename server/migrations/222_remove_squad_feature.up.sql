-- Remove legacy Squad product surface from the backend.
-- Historical squad-assigned issues become unassigned; squad-assigned autopilots
-- are removed because there is no safe agent owner once the squad table is gone.

UPDATE issue
SET assignee_type = NULL,
    assignee_id = NULL,
    updated_at = now()
WHERE assignee_type = 'squad';

DELETE FROM autopilot
WHERE assignee_type = 'squad';

ALTER TABLE issue DROP CONSTRAINT IF EXISTS issue_assignee_type_check;
ALTER TABLE issue ADD CONSTRAINT issue_assignee_type_check
    CHECK (assignee_type IN ('member', 'agent'));

UPDATE autopilot_run SET squad_id = NULL WHERE squad_id IS NOT NULL;

DROP INDEX IF EXISTS idx_autopilot_assignee_type_id;
ALTER TABLE autopilot DROP CONSTRAINT IF EXISTS autopilot_assignee_type_check;
ALTER TABLE autopilot ADD CONSTRAINT autopilot_assignee_type_check
    CHECK (assignee_type = 'agent');
CREATE INDEX IF NOT EXISTS idx_autopilot_assignee_type_id
    ON autopilot (assignee_type, assignee_id);

DELETE FROM squad_member;
DELETE FROM squad;
