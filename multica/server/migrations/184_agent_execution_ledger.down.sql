-- Irreversible by design (PR #673): inbox execution IDs have no lossless
-- queue-row representation. Preserve the ledger rather than deleting billing
-- evidence during a rollback. The matching application rollout is forward-only.
--
-- Task #108 (2026-08-02): this file used to be comments only, so migrate-down
-- treated it as a successful no-op. Rollback then continued into migration 182,
-- which RENAMEs triggers that 184.up.sql already DROPped, and surfaces a raw
-- Postgres error that looks like "182 is broken":
--   ERROR: trigger "trg_issue_project_dirty_agent_usage_hourly" for table "issue"
--   does not exist (SQLSTATE 42704)
-- That symptom points at the wrong layer — 182 is fine; 184 already removed the
-- objects 182's down expects. Fail loud here instead. Do NOT turn this into a
-- real reverse migration (separate product decision; needs its own card).
DO $$
BEGIN
    RAISE EXCEPTION 'migration 184 down cannot proceed: agent_execution ledger is irreversible by design (PR #673 / task #108). Inbox execution IDs have no lossless queue-row representation, and the forward-only app rollout now depends on this ledger — rolling it back would destroy or mis-shape billing evidence. This file used to be a comment-only no-op; that let migrate-down silently pass 184 and then fail later in 182 with ERROR: trigger "trg_issue_project_dirty_agent_usage_hourly" for table "issue" does not exist — which means 184 already dropped those triggers, not that 182 is broken. If you insist on rolling back past 184, stop here and manually restore everything 184.up.sql removed before continuing (at minimum recreate triggers trg_atq_dirty_agent_usage_hourly on agent_task_queue, trg_issue_delete_dirty_agent_usage_hourly on issue, trg_issue_project_dirty_agent_usage_hourly on issue, plus their enqueue_agent_usage_hourly_dirty_for_* functions from the post-182 / pre-184 state; also decide what to do with agent_execution rows and the dropped agent_usage FK). There is no safe automated path in this down.sql.';
END $$;
