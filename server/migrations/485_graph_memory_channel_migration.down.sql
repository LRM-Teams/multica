-- Task 16 down: remove the binding guard, the copy/redirect/blob ledgers,
-- and the channel_migration phase gate. Route and lineage rows, channel
-- bindings already applied to channel.project_id, and any copied artifacts
-- stay (they are live data, not migration scaffolding).
BEGIN;

DROP TRIGGER IF EXISTS graph_memory_channel_binding_guard_trigger ON channel;
DROP FUNCTION IF EXISTS graph_memory_channel_binding_guard();

DROP TABLE IF EXISTS graph_memory_migration_blob_ref;
DROP TABLE IF EXISTS graph_memory_migration_redirect;
DROP TABLE IF EXISTS graph_memory_channel_migration_state;
DROP TABLE IF EXISTS graph_memory_channel_binding;

ALTER TABLE memory_read_phase_gate
    DROP CONSTRAINT IF EXISTS memory_read_phase_gate_transition_check;
ALTER TABLE memory_read_phase_gate
    ADD CONSTRAINT memory_read_phase_gate_transition_check CHECK (
        (atoms_enabled OR search_v2_enabled OR explore_enabled
         OR citations_enabled OR atom_consolidation_enabled)
        <= retraction_canary_ok
    );
ALTER TABLE memory_read_phase_gate
    DROP COLUMN IF EXISTS channel_migration_enabled;

COMMIT;
