DROP INDEX IF EXISTS sandbox_node_owner_idx;

ALTER TABLE sandbox_node
    DROP COLUMN IF EXISTS deleted_at,
    DROP COLUMN IF EXISTS owner_user_id;
