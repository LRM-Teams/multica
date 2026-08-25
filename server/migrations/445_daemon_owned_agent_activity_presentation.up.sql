-- Activity is best-effort daemon observation, not a server-ordered lifecycle ledger.
DROP TABLE IF EXISTS agent_activity_probe;

ALTER TABLE agent_activity_snapshot
  ADD COLUMN IF NOT EXISTS summary_label TEXT NOT NULL DEFAULT 'Working...',
  DROP COLUMN IF EXISTS process_instance_id,
  DROP COLUMN IF EXISTS client_sequence,
  DROP COLUMN IF EXISTS producer_fact_id,
  DROP COLUMN IF EXISTS probe_id;

ALTER TABLE agent_activity_entry
  ADD COLUMN IF NOT EXISTS title TEXT NOT NULL DEFAULT 'Working',
  ADD COLUMN IF NOT EXISTS subtext TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS activity_kind TEXT NOT NULL DEFAULT 'working',
  ADD COLUMN IF NOT EXISTS detail_kind TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS body_kind TEXT NOT NULL DEFAULT 'generic',
  ADD COLUMN IF NOT EXISTS body TEXT NOT NULL DEFAULT '',
  DROP COLUMN IF EXISTS process_instance_id,
  DROP COLUMN IF EXISTS client_sequence,
  DROP COLUMN IF EXISTS producer_fact_id,
  DROP COLUMN IF EXISTS entry_position,
  DROP COLUMN IF EXISTS entry_kind,
  DROP COLUMN IF EXISTS entry_body;
