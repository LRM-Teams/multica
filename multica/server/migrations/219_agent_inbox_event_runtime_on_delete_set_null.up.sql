-- LRM-438 / LRM-437: agent_inbox_event.runtime_id was added in 183 without an
-- ON DELETE clause (defaults to NO ACTION). Historical inbox rows keep an
-- immutable runtime snapshot, so DELETE FROM agent_runtime fails with FK
-- violation and Computer bulk-delete surfaces as generic
-- "failed to delete runtimes" (Frank IMG_3127). Align with other nullable
-- runtime FKs (session, delivery, activity, …): null the snapshot on delete.
ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_runtime_id_fkey;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_runtime_id_fkey
  FOREIGN KEY (runtime_id) REFERENCES agent_runtime(id) ON DELETE SET NULL;
