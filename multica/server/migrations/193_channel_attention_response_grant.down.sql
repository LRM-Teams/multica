DROP TABLE IF EXISTS channel_attention_response_grant;
DROP TABLE IF EXISTS channel_attention_convergence_vote;
DROP TABLE IF EXISTS channel_attention_contribution_offer;

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT IF EXISTS agent_inbox_event_response_mode_check;

UPDATE agent_inbox_event
SET response_mode = 'no_public_output'
WHERE response_mode = 'convergence_vote';

ALTER TABLE agent_inbox_event
  ADD CONSTRAINT agent_inbox_event_response_mode_check
    CHECK (response_mode IN ('no_public_output', 'contribution_offer', 'public_response'));
