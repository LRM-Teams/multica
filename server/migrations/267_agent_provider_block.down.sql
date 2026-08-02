ALTER TABLE agent
  DROP COLUMN IF EXISTS provider_blocked_until,
  DROP COLUMN IF EXISTS provider_block_reason,
  DROP COLUMN IF EXISTS provider_block_detail;
