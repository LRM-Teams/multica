BEGIN;

-- Irreversible data heal: we cannot distinguish agents backfilled here from
-- agents that joined #general via the live trigger / ensure path. Rolling
-- back would silently drop legitimate roster rows. Leave the function body
-- as a no-op and keep the version row for audit.

COMMIT;
