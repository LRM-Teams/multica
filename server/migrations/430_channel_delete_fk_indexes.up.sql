-- Deleting a group channel cascades through its messages, threads, members and
-- every row that anchors to them. PostgreSQL enforces each CASCADE / SET NULL
-- with one child lookup per deleted parent row, so an FK column without a
-- supporting index turns the teardown into a sequential scan per message.
-- On the test workspace a 7.5k-message channel needed ~200s (dominated by
-- chat_message.channel_thread_root_message_id scanning a 1.6 GB table 7470
-- times) and the request died on the 30s proxy timeout as a 500. With these
-- indexes the same delete takes 3.9s.
--
-- Every column below is an FK inside the ON DELETE CASCADE closure rooted at
-- `channel`; cmd/migrate/channel_delete_fk_indexes_test.go walks that closure
-- and fails if a future table reintroduces an unindexed edge.
--
-- These are partial indexes on mostly-NULL columns, so the builds are
-- sub-second even on the largest table (chat_message, 1.6 GB → 71ms). Plain
-- CREATE INDEX keeps the definitions in the migration where they are readable;
-- the CONCURRENTLY + pre-migration-hook machinery used by 227/229/425 is not
-- worth its indirection at this size.

CREATE INDEX IF NOT EXISTS idx_agent_action_card_archive_channel_id ON agent_action_card_archive (channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_attachment_upload_session_channel_id ON agent_attachment_upload_session (channel_id);
CREATE INDEX IF NOT EXISTS idx_agent_attachment_upload_session_thread_root_message_id ON agent_attachment_upload_session (thread_root_message_id) WHERE thread_root_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_creation_draft_channel_id ON agent_creation_draft (channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_dm_exchange_latest_message_id ON agent_dm_exchange (latest_message_id) WHERE latest_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_dm_exchange_source_channel_id ON agent_dm_exchange (source_channel_id) WHERE source_channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_dm_exchange_source_message_id ON agent_dm_exchange (source_message_id) WHERE source_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_inbox_event_channel_id ON agent_inbox_event (channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_message_delivery_message_id ON agent_message_delivery (message_id);
CREATE INDEX IF NOT EXISTS idx_agent_reminder_anchor_message_id ON agent_reminder (anchor_message_id) WHERE anchor_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_reminder_anchor_thread_root_message_id ON agent_reminder (anchor_thread_root_message_id) WHERE anchor_thread_root_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_session_channel_id ON agent_session (channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_agent_session_conversation_id ON agent_session (conversation_id) WHERE conversation_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_channel_attention_round_trigger_message_id ON channel_attention_round (trigger_message_id) WHERE trigger_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_channel_decision_audit_message_id ON channel_decision_audit (message_id) WHERE message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_channel_goal_process_markdown_channel_id ON channel_goal_process_markdown (channel_id);
CREATE INDEX IF NOT EXISTS idx_channel_message_thread_root_message_id ON channel_message (thread_root_message_id) WHERE thread_root_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_channel_thread_state_channel_id ON channel_thread_state (channel_id);
CREATE INDEX IF NOT EXISTS idx_channel_voice_synthesis_channel_id ON channel_voice_synthesis (channel_id);
CREATE INDEX IF NOT EXISTS idx_channel_voice_transcription_channel_id ON channel_voice_transcription (channel_id);
CREATE INDEX IF NOT EXISTS idx_chat_message_channel_thread_root_message_id ON chat_message (channel_thread_root_message_id) WHERE channel_thread_root_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_collaboration_session_channel_id ON collaboration_session (channel_id);
CREATE INDEX IF NOT EXISTS idx_collaboration_session_source_message_id ON collaboration_session (source_message_id) WHERE source_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_collaboration_turn_result_message_id ON collaboration_turn (result_message_id) WHERE result_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_env_dispatch_run_local_channel_id ON env_dispatch_run (local_channel_id) WHERE local_channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_evolution_training_example_channel_id ON evolution_training_example (channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_evolution_training_example_message_id ON evolution_training_example (message_id) WHERE message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_issue_source_message_channel_id ON issue_source_message (channel_id);
CREATE INDEX IF NOT EXISTS idx_issue_source_message_message_id ON issue_source_message (message_id) WHERE message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_note_worker_job_channel_message_id ON note_worker_job (channel_message_id) WHERE channel_message_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_pi_message_consumption_channel_message_id ON pi_message_consumption (channel_message_id);
CREATE INDEX IF NOT EXISTS idx_research_session_channel_id ON research_session (channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_skill_promotion_channel_id ON skill_promotion (channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_thread_participant_conversation_id ON thread_participant (conversation_id);
CREATE INDEX IF NOT EXISTS idx_voice_call_session_channel_id ON voice_call_session (channel_id);
CREATE INDEX IF NOT EXISTS idx_work_node_primary_channel_id ON work_node (primary_channel_id) WHERE primary_channel_id IS NOT NULL;
