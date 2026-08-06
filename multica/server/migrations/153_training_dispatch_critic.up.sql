-- sub-project E: persist critic_agent_id on training_dispatch so the
-- auto-spawn hook (T7) and deferred close hook (T8) can resolve the critic
-- by project_id. Nullable: empty critic_agent_id means unchanged behavior.
ALTER TABLE training_dispatch
    ADD COLUMN critic_agent_id UUID NULL;
