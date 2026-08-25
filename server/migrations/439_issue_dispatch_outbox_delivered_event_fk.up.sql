-- A delivered outbox row is durable audit history, while its Inbox Run may be
-- removed later by retention or test cleanup. Keep the outbox and clear only
-- the optional event pointer when that happens.
ALTER TABLE issue_dispatch_outbox
  DROP CONSTRAINT issue_dispatch_outbox_check1,
  DROP CONSTRAINT issue_dispatch_outbox_delivered_event_id_fkey;

ALTER TABLE issue_dispatch_outbox
  ADD CONSTRAINT issue_dispatch_outbox_delivered_contract_check CHECK (
    (status = 'delivered' AND delivered_at IS NOT NULL)
    OR (status <> 'delivered' AND delivered_event_id IS NULL AND delivered_at IS NULL)
  ),
  ADD CONSTRAINT issue_dispatch_outbox_delivered_event_id_fkey
    FOREIGN KEY (delivered_event_id)
    REFERENCES agent_inbox_event(id)
    ON DELETE SET NULL;
