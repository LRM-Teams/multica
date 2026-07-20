-- Retire the Channel Attention Round feature. Collaboration (PR-4/5) and the
-- legacy Andong wake-all ambient path are unaffected: they never used the
-- attention delivery_mode/reason values or the channel_attention_* tables.

-- Stop any in-flight attention leases before the rows disappear. Migrations
-- run while the server is offline, so no new lease can race this cleanup.
UPDATE agent_event_delivery delivery
SET status = 'expired',
    last_error = 'channel attention feature removed',
    updated_at = now()
FROM agent_inbox_event event
WHERE delivery.inbox_event_id = event.id
  AND (event.delivery_mode = 'attention'
       OR event.reason IN ('attention_response_grant', 'attention_convergence', 'attention_manager_fallback'))
  AND delivery.status IN ('leased', 'processing');

-- Remove attention inbox events outright rather than merely suppressing them:
-- their reason/delivery_mode values are being dropped from the check
-- constraints below, so no row may keep them around for audit purposes.
DELETE FROM agent_inbox_event
WHERE delivery_mode = 'attention'
   OR reason IN ('attention_response_grant', 'attention_convergence', 'attention_manager_fallback');

-- Drop the Channel Attention Round tables in FK-safe (children-first) order.
DROP TABLE IF EXISTS channel_attention_response_grant;
DROP TABLE IF EXISTS channel_attention_convergence_vote;
DROP TABLE IF EXISTS channel_attention_contribution_offer;
DROP TABLE IF EXISTS channel_attention_dispatch_outbox;
DROP TABLE IF EXISTS channel_attention_participant;
DROP TABLE IF EXISTS channel_attention_round;

-- delivery_mode no longer has a restricted "attention" mode; only ambient
-- observe events and normal execute wakes remain.
ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_delivery_mode_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_delivery_mode_check
    CHECK (delivery_mode IN ('observe', 'execute'));

-- response_mode no longer needs the attention-only contribution_offer /
-- convergence_vote states.
ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_response_mode_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_response_mode_check
    CHECK (response_mode IN ('no_public_output', 'public_response'));

-- reason keeps the ambient/mention/dm wake reasons plus Collaboration's own
-- turn and manager-fallback reasons; the attention_* reasons are gone.
ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_reason_check
  CHECK (reason IN (
    'mention',
    'dm',
    'ambient',
    'thread_reply',
    'channel_message',
    'collaboration_turn',
    'collaboration_manager_fallback'
  ));

-- channel_decision_audit source_kind: drop the attention-only sources, keep
-- the generic agent_transport / collaboration_session / collaboration_turn
-- sources that are unrelated to Channel Attention Round.
ALTER TABLE channel_decision_audit
  DROP CONSTRAINT IF EXISTS channel_decision_audit_source_kind_check;

ALTER TABLE channel_decision_audit
  ADD CONSTRAINT channel_decision_audit_source_kind_check
    CHECK (source_kind IN ('collaboration_session', 'collaboration_turn', 'agent_transport'));
