-- LRM-1007: optional source message for Goal subgoal "jump back" links.
-- Same-channel membership is enforced in the API; FK keeps referential integrity.

ALTER TABLE channel_goal_subgoal
  ADD COLUMN IF NOT EXISTS source_message_id UUID
    REFERENCES channel_message(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS channel_goal_subgoal_source_message
  ON channel_goal_subgoal (source_message_id)
  WHERE source_message_id IS NOT NULL;
