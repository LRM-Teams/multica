BEGIN;

ALTER TABLE machine_upgrade
    DROP COLUMN IF EXISTS attested_runtime_ids,
    DROP COLUMN IF EXISTS accepted_runtime_ids,
    DROP COLUMN IF EXISTS accepted_generation;

COMMIT;
