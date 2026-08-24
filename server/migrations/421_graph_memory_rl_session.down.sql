DROP TRIGGER IF EXISTS graph_memory_reward_outbox_identity ON graph_memory_reward_outbox;
DROP FUNCTION IF EXISTS graph_memory_reward_outbox_validate_identity();
DROP TRIGGER IF EXISTS graph_memory_rl_session_identity ON graph_memory_rl_session;
DROP FUNCTION IF EXISTS graph_memory_rl_session_validate_identity();
DROP TABLE IF EXISTS graph_memory_reward_outbox;
DROP TABLE IF EXISTS graph_memory_rl_session;
