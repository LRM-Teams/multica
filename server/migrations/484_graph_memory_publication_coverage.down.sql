-- Task 14 down: remove the DB-authoritative publication ledger and the
-- atom_consolidation phase-gate route. File-store `current` pointers are
-- untouched (they were already only a cache by the time this landed).
ALTER TABLE memory_read_phase_gate
    DROP CONSTRAINT IF EXISTS memory_read_phase_gate_transition_check;
ALTER TABLE memory_read_phase_gate
    ADD CONSTRAINT memory_read_phase_gate_transition_check CHECK (
        (atoms_enabled OR search_v2_enabled OR explore_enabled OR citations_enabled)
        <= retraction_canary_ok
    );
ALTER TABLE memory_read_phase_gate
    DROP COLUMN IF EXISTS atom_consolidation_enabled;

DROP TABLE IF EXISTS graph_memory_publication_outcome;
DROP TABLE IF EXISTS graph_memory_publication_provenance;
DROP TABLE IF EXISTS graph_memory_publication_coverage;
DROP TABLE IF EXISTS graph_memory_publication_index;
DROP TABLE IF EXISTS graph_memory_publication;
