ALTER TABLE thread_participant
  DROP CONSTRAINT IF EXISTS thread_participant_wake_state_check;

ALTER TABLE thread_participant
  ADD CONSTRAINT thread_participant_wake_state_check
  CHECK (wake_state IN ('active', 'no_wake', 'unfollowed', 'removed'));

-- `no_wake` was also created for directed thread deliveries, so it cannot by
-- itself prove an explicit opt-out. Backfill only rows with a canonical
-- thread-unfollow system fact or agent transport audit.
UPDATE thread_participant participant
SET wake_state = 'unfollowed', updated_at = now()
WHERE participant.member_type = 'agent'
  AND participant.followed_at IS NULL
  AND participant.wake_state = 'no_wake'
  AND (
    EXISTS (
      SELECT 1
      FROM channel_message message
      CROSS JOIN LATERAL jsonb_array_elements(COALESCE(message.parts, '[]'::jsonb)) AS part(value)
      WHERE message.thread_root_message_id = participant.root_message_id
        AND message.author_type = 'system'
        AND part.value->>'type' = 'system_event'
        AND part.value->>'event' = 'thread_unfollowed'
        AND COALESCE(part.value->'event_params'->>'agent_id', part.value->'event_params'->>'actor_id') = participant.member_id::text
    )
    OR EXISTS (
      SELECT 1
      FROM agent_task_transport_audit audit
      WHERE audit.action = 'thread_unfollow'
        AND audit.channel_message_id = participant.root_message_id
        AND audit.agent_id = participant.member_id
    )
  );
