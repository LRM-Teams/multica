DELETE FROM agent_radar_action
WHERE action_type = 'request_rework';

ALTER TABLE agent_radar_action
  DROP CONSTRAINT agent_radar_action_action_type_check;

ALTER TABLE agent_radar_action
  ADD CONSTRAINT agent_radar_action_action_type_check
  CHECK (action_type IN (
    'no_action',
    'post_channel_message',
    'reply_thread',
    'mention_agent',
    'create_issue',
    'comment_issue',
    'assign_issue',
    'schedule_reminder',
    'update_agent_plan'
  ));
