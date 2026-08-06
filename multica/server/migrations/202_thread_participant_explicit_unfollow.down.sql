UPDATE thread_participant
SET wake_state = 'no_wake', updated_at = now()
WHERE wake_state = 'unfollowed';

ALTER TABLE thread_participant
  DROP CONSTRAINT IF EXISTS thread_participant_wake_state_check;

ALTER TABLE thread_participant
  ADD CONSTRAINT thread_participant_wake_state_check
  CHECK (wake_state IN ('active', 'no_wake', 'removed'));
