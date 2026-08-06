BEGIN;

-- #2378 records the daemon process that accepted the operation and the exact
-- managed runtime set it promised to converge. These are deliberately on the
-- canonical machine operation: sibling runtime projections must not create
-- independently completable state.
ALTER TABLE machine_upgrade
    ADD COLUMN accepted_generation TEXT,
    ADD COLUMN accepted_runtime_ids UUID[] NOT NULL DEFAULT '{}',
    ADD COLUMN attested_runtime_ids UUID[] NOT NULL DEFAULT '{}';

COMMIT;
