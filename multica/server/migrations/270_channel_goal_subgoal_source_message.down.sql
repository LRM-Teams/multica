DROP INDEX IF EXISTS channel_goal_subgoal_source_message;

ALTER TABLE channel_goal_subgoal
  DROP COLUMN IF EXISTS source_message_id;
