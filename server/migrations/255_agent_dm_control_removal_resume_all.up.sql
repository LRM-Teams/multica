-- Task #813/#830 follow-up (Frank, 2026-07-31, #prj-daemon): all four
-- agent-agent DM pause gates (round budget, frequency, manual pair pause,
-- owner global pause) are retired in the application layer as of this
-- migration's release. The resume/grant-rounds admin actions that used to be
-- the only way to un-stick a paused exchange are gone too — so any exchange
-- already sitting in a non-active state right now would be stuck forever
-- with no code path left to resume it. Flip everything to active once, here,
-- so the removal doesn't strand pre-existing conversations.
--
-- Not dropped: the `state`/`pause_reason` columns and their CHECK
-- constraints, and the agent_dm_pair_control/agent_dm_owner_control tables
-- themselves. They're inert (nothing in the application layer writes
-- non-active states anymore), but dropping them is a separate, larger
-- schema-cleanup decision — this migration only unblocks stuck data.
UPDATE agent_dm_exchange
SET state = 'active',
    pause_reason = NULL,
    updated_at = now()
WHERE state <> 'active';

UPDATE agent_dm_pair_control
SET state = 'active',
    pause_reason = NULL,
    updated_at = now()
WHERE state <> 'active';

UPDATE agent_dm_owner_control
SET paused = false,
    pause_reason = NULL,
    updated_at = now()
WHERE paused = true;
