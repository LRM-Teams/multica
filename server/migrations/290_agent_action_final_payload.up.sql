-- Persist the non-sensitive final configuration selected by the human who
-- commits an agent:create Proposal. The canonical Message itself remains
-- limited to the agent-authored proposal fields.
ALTER TABLE agent_action
    ADD COLUMN final_payload JSONB;
