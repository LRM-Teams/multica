DROP INDEX IF EXISTS idx_agent_event_delivery_session_active;
DROP INDEX IF EXISTS idx_agent_event_delivery_event_created;
DROP TABLE IF EXISTS agent_event_delivery;

DROP INDEX IF EXISTS idx_agent_inbox_event_ambient_pending_unique;
DROP INDEX IF EXISTS idx_agent_inbox_event_source_message;
DROP INDEX IF EXISTS idx_agent_inbox_event_conversation;
DROP INDEX IF EXISTS idx_agent_inbox_event_pending;
DROP TABLE IF EXISTS agent_inbox_event;

DROP INDEX IF EXISTS idx_agent_session_chat_session;
DROP INDEX IF EXISTS idx_agent_session_runtime_active;
DROP TABLE IF EXISTS agent_session;
