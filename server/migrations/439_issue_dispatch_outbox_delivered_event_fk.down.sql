-- A deleted Inbox Run cannot be reconstructed safely. Refuse a destructive
-- rollback if ON DELETE SET NULL has already been exercised.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM issue_dispatch_outbox
    WHERE status = 'delivered'
      AND delivered_event_id IS NULL
  ) THEN
    RAISE EXCEPTION 'cannot restore delivered_event_id RESTRICT contract: delivered Inbox Run history has already been removed';
  END IF;
END
$$;

ALTER TABLE issue_dispatch_outbox
  DROP CONSTRAINT issue_dispatch_outbox_delivered_contract_check,
  DROP CONSTRAINT issue_dispatch_outbox_delivered_event_id_fkey;

ALTER TABLE issue_dispatch_outbox
  ADD CONSTRAINT issue_dispatch_outbox_delivered_contract_check CHECK (
    (status = 'delivered' AND delivered_event_id IS NOT NULL AND delivered_at IS NOT NULL)
    OR (status <> 'delivered' AND delivered_event_id IS NULL AND delivered_at IS NULL)
  ),
  ADD CONSTRAINT issue_dispatch_outbox_delivered_event_id_fkey
    FOREIGN KEY (delivered_event_id)
    REFERENCES agent_inbox_event(id);
