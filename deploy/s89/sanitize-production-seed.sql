\set ON_ERROR_STOP on

-- Sanitize a production database snapshot before the Tencent test backend is
-- allowed to start. Run this with the test PostgreSQL offline from every
-- Multica application process. Business records (users, workspaces, agents,
-- issues, messages, comments, and completed history) are retained; copied
-- credentials and executable/leased state are not.
--
-- This script is intentionally idempotent. A failed statement aborts the
-- transaction, so the test database never lands in a partially sanitized
-- state.

BEGIN;
SET LOCAL lock_timeout = '30s';
SET LOCAL statement_timeout = '20min';

-- Browser, API, Computer, Agent, task, sandbox, and device credentials.
DELETE FROM verification_code;
DELETE FROM device_authorization;
DELETE FROM daemon_token;
DELETE FROM task_token;
DELETE FROM agent_inbox_token;
DELETE FROM lark_binding_token;

UPDATE personal_access_token
   SET revoked = TRUE,
       token_hash = 'test-disabled-pat-' || id::text,
       token_prefix = 'test_disabled',
       last_used_at = NULL;

UPDATE agent_credential
   SET revoked_at = COALESCE(revoked_at, now()),
       disabled_at = COALESCE(disabled_at, now()),
       token_hash = 'test-disabled-agent-' || id::text,
       token_prefix = 'test_disabled',
       last_used_at = NULL,
       updated_at = now();

UPDATE sandbox_node_token
   SET revoked_at = COALESCE(revoked_at, now()),
       token_hash = 'test-disabled-sandbox-' || id::text,
       token_prefix = 'test_disabled';

DELETE FROM computer_workspace_bindings;

-- Test must establish its own Computer/runtime observations and upgrade
-- state. Historical completed Machine Upgrade rows remain as history.
DELETE FROM daemon_heartbeat;
DELETE FROM daemon_update_status;
DELETE FROM daemon_runtime_update_intent;
DELETE FROM daemon_runtime_update;
DELETE FROM daemon_registration_tombstone;

UPDATE agent_runtime
   SET status = 'offline',
       last_seen_at = NULL,
       starting_since = NULL,
       offline_reason = 'test_database_copy',
       updated_at = now();

UPDATE machine_upgrade
   SET phase = 'cancelled',
       result = 'cancelled',
       error_code = 'TEST_DATABASE_COPY',
       error_message = 'Cancelled while sanitizing a production snapshot for test',
       completed_at = COALESCE(completed_at, now()),
       updated_at = now()
 WHERE phase NOT IN ('completed', 'failed', 'rolled_back', 'timeout', 'cancelled');

-- Agent-local provider sessions, work directories, custom environment values,
-- MCP configuration, and copied transient status do not cross environments.
UPDATE agent
   SET custom_env = '{}'::jsonb,
       custom_args = '[]'::jsonb,
       mcp_config = NULL,
       crashed_since = NULL,
       provider_blocked_until = NULL,
       provider_block_detail = '{}'::jsonb,
       status = CASE WHEN status IN ('working', 'blocked') THEN 'idle' ELSE status END,
       updated_at = now();

UPDATE agent_session
   SET status = 'closed',
       lease_token = NULL,
       lease_expires_at = NULL,
       updated_at = now()
 WHERE status <> 'closed';

UPDATE agent_inbox_event
   SET status = 'suppressed',
       terminal_outcome = 'cancelled',
       terminal_at = COALESCE(terminal_at, now()),
       completed_at = COALESCE(completed_at, now()),
       retryable = FALSE,
       last_error = 'Suppressed while sanitizing a production snapshot for test',
       updated_at = now()
 WHERE status IN ('pending', 'draining', 'failed');

UPDATE agent_event_delivery
   SET status = 'expired',
       lease_expires_at = LEAST(lease_expires_at, now()),
       last_error = 'Expired while sanitizing a production snapshot for test',
       updated_at = now()
 WHERE status IN ('leased', 'processing');

UPDATE agent_execution
   SET status = 'cancelled',
       completed_at = COALESCE(completed_at, now()),
       failure_reason = 'test_database_copy',
       error = 'Cancelled while sanitizing a production snapshot for test'
 WHERE status = 'running';

UPDATE agent_lifecycle_operation
   SET status = 'failed',
       step = 'test_database_copy',
       reason_code = 'test_database_copy',
       started_at = COALESCE(started_at, now()),
       finished_at = COALESCE(finished_at, now()),
       updated_at = now()
 WHERE status IN ('scheduled', 'running');

UPDATE agent_start_intent
   SET status = 'failed',
       failed_at = COALESCE(failed_at, now()),
       failure_code = 'test_database_copy',
       updated_at = now()
 WHERE status IN ('pending', 'accepted', 'queued', 'ready');

-- A prepared Agent creation action is an uncommitted authorization, not
-- history. Executed actions remain.
DELETE FROM agent_action WHERE status = 'prepared';

UPDATE agent_reminder
   SET status = 'cancelled',
       terminal_reason = 'test_database_copy',
       updated_at = now()
 WHERE status IN ('scheduled', 'firing');

UPDATE agent_reminder_occurrence
   SET status = 'cancelled',
       terminal_reason = 'test_database_copy',
       updated_at = now()
 WHERE status IN ('pending', 'claimed');

UPDATE channel_agent_onboarding
   SET status = 'expired',
       publication_lease_expires_at = NULL,
       terminal_at = COALESCE(terminal_at, now()),
       terminal_evidence = jsonb_build_object('reason', 'test_database_copy'),
       updated_at = now()
 WHERE status IN ('pending', 'claimed');

UPDATE agent_attachment_upload_session
   SET state = 'cancelled',
       cancelled_at = COALESCE(cancelled_at, now()),
       failure_code = 'test_database_copy'
 WHERE state = 'pending';

-- External integrations are copied as display/history only and must be
-- explicitly reconnected in test.
UPDATE web_push_subscription
   SET endpoint = 'test-disabled://' || id::text,
       p256dh = '',
       auth = '',
       revoked_at = COALESCE(revoked_at, now()),
       last_error = 'Disabled while sanitizing a production snapshot for test',
       updated_at = now();

UPDATE lark_installation
   SET status = 'revoked',
       app_id = 'test-disabled-' || id::text,
       app_secret_encrypted = '\x'::bytea,
       tenant_key = NULL,
       bot_open_id = 'test-disabled-' || id::text,
       bot_union_id = NULL,
       ws_lease_token = NULL,
       ws_lease_expires_at = NULL,
       updated_at = now();

UPDATE workspace_invitation
   SET status = 'expired',
       expires_at = LEAST(expires_at, now()),
       updated_at = now()
 WHERE status = 'pending';

-- Sandbox nodes and instances refer to infrastructure that exists only in
-- production. Preserve their records, but revoke execution authority.
UPDATE sandbox_node
   SET status = 'offline',
       last_seen_at = NULL,
       updated_at = now()
 WHERE status <> 'offline';

UPDATE sandbox_workspace_binding
   SET enabled = FALSE,
       updated_at = now()
 WHERE enabled;

UPDATE sandbox_job
   SET status = CASE WHEN status IN ('queued', 'dispatched', 'running') THEN 'cancelled' ELSE status END,
       error = CASE WHEN status IN ('queued', 'dispatched', 'running') THEN 'Cancelled while sanitizing a production snapshot for test' ELSE error END,
       lease_until = NULL,
       completed_at = CASE WHEN status IN ('queued', 'dispatched', 'running') THEN COALESCE(completed_at, now()) ELSE completed_at END,
       job_token_hash = NULL,
       job_token_expires_at = NULL,
       updated_at = now();

UPDATE sandbox_instance
   SET status = 'failed',
       error = 'Production sandbox is unavailable in test',
       updated_at = now()
 WHERE status IN ('pending', 'creating', 'running', 'stopping', 'resuming', 'reconfiguring', 'snapshotting');

UPDATE environment_agent_sandbox
   SET status = CASE WHEN status IN ('deleted', 'failed') THEN status ELSE 'failed' END,
       sandbox_instance_id = NULL,
       runtime_id = NULL,
       daemon_id = NULL,
       sandbox_config = '{}'::jsonb,
       last_error = 'Production sandbox is unavailable in test',
       updated_at = now();

-- Pause resumable business workflows, and cancel their currently executable
-- work/leases. Completed research and historical evidence remain unchanged.
UPDATE research_session
   SET status = 'paused',
       unattended_enabled = FALSE,
       reconcile_lease_token = NULL,
       reconcile_lease_expires_at = NULL,
       stop_reason = 'test_database_copy',
       last_error = '',
       updated_at = now()
 WHERE status IN ('drafting', 'running', 'awaiting_user_confirm');

-- The canonical Research state machine only permits blocked -> ready ->
-- cancelled. Keep the sanitizer inside that same contract.
UPDATE research_task
   SET status = 'ready',
       ready_at = now(),
       updated_at = now()
 WHERE status = 'blocked';

UPDATE research_task
   SET status = 'cancelled',
       terminal_reason = 'test_database_copy',
       completed_at = COALESCE(completed_at, now()),
       updated_at = now()
 WHERE status IN ('pending', 'ready', 'dispatching', 'running', 'blocked');

UPDATE research_task_attempt
   SET status = 'cancelled',
       cancellation_requested_at = COALESCE(cancellation_requested_at, now()),
       cancellation_completed_at = COALESCE(cancellation_completed_at, now()),
       completed_at = COALESCE(completed_at, now()),
       runtime_lease_expires_at = NULL,
       pending_failure_class = NULL,
       pending_failure_diagnostics = NULL,
       pending_failure_retryable = NULL,
       updated_at = now()
 WHERE status IN ('dispatching', 'running', 'cancelling');

UPDATE research_dispatch_outbox
   SET status = 'cancelled',
       lease_token = NULL,
       lease_expires_at = NULL,
       last_error = 'Cancelled while sanitizing a production snapshot for test',
       updated_at = now()
 WHERE status IN ('pending', 'delivering');

UPDATE research_work_item
   SET status = 'cancelled',
       reason = 'test_database_copy',
       completed_at = COALESCE(completed_at, now()),
       updated_at = now()
 WHERE status IN ('pending', 'enqueued');

UPDATE memory_curation_run
   SET status = 'cancelled',
       error = 'Cancelled while sanitizing a production snapshot for test',
       claim_token = NULL,
       finished_at = COALESCE(finished_at, now())
 WHERE status IN ('queued', 'waiting_runtime', 'running');

UPDATE memory_curation_agent_run
   SET status = 'cancelled',
       error = 'Cancelled while sanitizing a production snapshot for test',
       claim_token = NULL,
       finished_at = COALESCE(finished_at, now()),
       updated_at = now()
 WHERE status IN ('queued', 'waiting_runtime', 'running');

UPDATE channel_goal
   SET status = 'paused',
       blocker = 'Paused while sanitizing a production snapshot for test',
       updated_at = now()
 WHERE status = 'active';

UPDATE goal_execution_epoch
   SET status = 'cancelled',
       lease_owner = NULL,
       lease_token = NULL,
       lease_expires_at = NULL,
       updated_at = now()
 WHERE status IN ('planned', 'running', 'evaluating', 'waiting');

UPDATE work_node
   SET status = 'cancelled',
       last_progress_summary = 'Cancelled while sanitizing a production snapshot for test',
       updated_at = now()
 WHERE status IN ('active', 'waiting', 'blocked', 'needs_rework');

UPDATE work_owner_lease
   SET status = 'expired',
       expires_at = LEAST(expires_at, now()),
       updated_at = now()
 WHERE status = 'active';

UPDATE migration_lease
   SET status = 'expired',
       expires_at = LEAST(expires_at, now()),
       updated_at = now()
 WHERE status = 'reserved';

COMMIT;

-- Postcondition summary. Every count below must be zero before backend start.
SELECT 'active_personal_access_tokens' AS check_name, count(*) AS remaining
  FROM personal_access_token
 WHERE NOT revoked AND (expires_at IS NULL OR expires_at > now())
UNION ALL SELECT 'active_agent_credentials', count(*) FROM agent_credential WHERE revoked_at IS NULL AND disabled_at IS NULL
UNION ALL SELECT 'agent_inbox_tokens', count(*) FROM agent_inbox_token
UNION ALL SELECT 'task_tokens', count(*) FROM task_token
UNION ALL SELECT 'online_runtimes', count(*) FROM agent_runtime WHERE status = 'online'
UNION ALL SELECT 'active_agent_sessions', count(*) FROM agent_session WHERE status = 'active'
UNION ALL SELECT 'executable_inbox_events', count(*) FROM agent_inbox_event WHERE status IN ('pending', 'draining', 'failed')
UNION ALL SELECT 'active_event_deliveries', count(*) FROM agent_event_delivery WHERE status IN ('leased', 'processing')
UNION ALL SELECT 'running_agent_executions', count(*) FROM agent_execution WHERE status = 'running'
UNION ALL SELECT 'active_reminders', count(*) FROM agent_reminder WHERE status IN ('scheduled', 'firing')
UNION ALL SELECT 'active_sandbox_nodes', count(*) FROM sandbox_node WHERE status = 'online'
UNION ALL SELECT 'active_sandbox_bindings', count(*) FROM sandbox_workspace_binding WHERE enabled
UNION ALL SELECT 'active_machine_upgrades', count(*) FROM machine_upgrade WHERE phase NOT IN ('completed', 'failed', 'rolled_back', 'timeout', 'cancelled')
UNION ALL SELECT 'active_computer_bindings', count(*) FROM computer_workspace_bindings WHERE active
UNION ALL SELECT 'daemon_heartbeats', count(*) FROM daemon_heartbeat
ORDER BY check_name;
