-- Bind only an unambiguous legacy onboarding Agent owned by the Workspace's
-- sole Owner. Existing bindings and ambiguous/empty candidate sets are left
-- unchanged. Runtime, model, Agent lifecycle state, and all unrelated Agents
-- are deliberately untouched.
WITH eligible AS (
    SELECT w.id AS workspace_id,
           (array_agg(a.id ORDER BY a.created_at, a.id))[1] AS agent_id
    FROM workspace w
    JOIN member owner_member
      ON owner_member.workspace_id = w.id
     AND owner_member.role = 'owner'
    JOIN agent a
      ON a.workspace_id = w.id
     AND a.owner_id = owner_member.user_id
     AND a.display_name IN ('Wendy', 'Windy', 'Joe')
    WHERE w.onboarding_agent_id IS NULL
    GROUP BY w.id
    HAVING count(*) = 1
)
UPDATE workspace w
SET onboarding_agent_id = eligible.agent_id,
    updated_at = now()
FROM eligible
WHERE w.id = eligible.workspace_id
  AND w.onboarding_agent_id IS NULL;
