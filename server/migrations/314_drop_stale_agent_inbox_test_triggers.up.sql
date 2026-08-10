-- Test suites historically installed fixture triggers on shared/dev DBs via
-- CREATE OR REPLACE FUNCTION + CREATE TRIGGER. An outdated copy of
-- test_agent_inbox_fixture_defaults rewrote reason='chat_session' → 'dm'
-- because its whitelist predated migration 313. After #2295, drain treats
-- reason='dm' as residual channel dual-write and suppresses it immediately —
-- so notes Space-AI / bubble chat timed out waiting for an assistant reply that
-- would never be written.
--
-- These triggers are not part of the production schema. Drop them; handler
-- TestMain recreates a corrected copy when tests run against a database.

DROP TRIGGER IF EXISTS test_agent_inbox_fixture_defaults ON agent_inbox_event;
DROP TRIGGER IF EXISTS test_server_agent_inbox_fixture_defaults ON agent_inbox_event;
DROP FUNCTION IF EXISTS test_agent_inbox_fixture_defaults();
DROP FUNCTION IF EXISTS test_server_agent_inbox_fixture_defaults();
