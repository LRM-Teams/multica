-- LRM-2343 cutover: canonical channel_message + agent_action is the only
-- live Proposal path. Preserve historical action cards for audit/rollback
-- instead of deleting them during the application migration.
ALTER TABLE IF EXISTS agent_action_card
    RENAME TO agent_action_card_archive;
