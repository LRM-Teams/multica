-- User-editable machine label for runtimes. Distinct from `name`, which the
-- daemon continues to upsert from hostname / reported provider name on
-- register. Empty display_name means "unset" — clients fall back to `name`.
ALTER TABLE agent_runtime
    ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
