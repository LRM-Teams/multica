BEGIN;

-- LRM-915 / follow-up to migration 251 (task #908):
-- 251 retired agent.visibility for #general eligibility but explicitly skipped
-- historical backfill ("历史数据不用管"). Agents that were private/owner-only
-- before that cut (notably Wendy) stay out of #general forever unless their
-- row is touched (UPDATE archived_at/workspace_id) or ensure_system_general_channel
-- runs. Prod symptom (Frank, 2026-08-03): "general 群里怎么看不到 wendy" —
-- member list / in-channel search empty while the agent still speaks elsewhere.
--
-- Re-run the canonical full-sync for every workspace that has a member who can
-- act as ensure()'s creator. Idempotent: ON CONFLICT DO NOTHING inside ensure,
-- and archived agents are pruned.

DO $$
DECLARE
  workspace_row RECORD;
BEGIN
  FOR workspace_row IN
    SELECT
      workspace.id AS workspace_id,
      (
        SELECT workspace_member.user_id
        FROM member workspace_member
        WHERE workspace_member.workspace_id = workspace.id
        ORDER BY
          CASE workspace_member.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END,
          workspace_member.created_at,
          workspace_member.id
        LIMIT 1
      ) AS creator_user_id
    FROM workspace
    ORDER BY workspace.id
  LOOP
    IF workspace_row.creator_user_id IS NULL THEN
      -- Orphan workspace with no members: nothing to sync into #general.
      CONTINUE;
    END IF;
    PERFORM ensure_system_general_channel(
      workspace_row.workspace_id,
      workspace_row.creator_user_id
    );
  END LOOP;
END;
$$;

COMMIT;
