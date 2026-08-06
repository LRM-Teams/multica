-- Ordinary chat Message delivery is canonical channel_message plus
-- agent_message_delivery. These task/inbox-scoped transport ledgers no longer
-- participate in any live request or recovery path.
DROP TABLE IF EXISTS agent_transport_draft;
DROP TABLE IF EXISTS agent_task_transport_audit;
DROP TABLE IF EXISTS channel_ambient_pending_wake;
