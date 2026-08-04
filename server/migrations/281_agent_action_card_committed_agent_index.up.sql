-- Supporting index for agent hard-delete SET NULL on committed_agent_id.
-- #2189 migration 280 added the FK without this index; agent_delete_fk_indexes
-- contract requires every agent FK to have a left-prefix matching index.
CREATE INDEX IF NOT EXISTS idx_agent_action_card_committed_agent
    ON agent_action_card (committed_agent_id)
    WHERE committed_agent_id IS NOT NULL;
