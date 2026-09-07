-- Down: restore the three-kind CHECK and drop the audit table. Applying
-- this down migration on a database that already contains new-kind atoms
-- fails loudly (the restored CHECK rejects them): production rollback
-- disables the writers rather than migrating down (ADR 0021 Decision 8),
-- and audit rows are kept in every enabled environment.
DROP TABLE graph_memory_kind_backfill_audit;

ALTER TABLE graph_memory_atom
    DROP CONSTRAINT graph_memory_atom_kind_check;

ALTER TABLE graph_memory_atom
    ADD CONSTRAINT graph_memory_atom_kind_check CHECK (
        kind IN ('fact', 'preference', 'fallback')
    );
