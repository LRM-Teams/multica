# Wendy onboarding-agent migration

Run this read-only preflight before applying migration 302. It classifies every
Workspace without changing bindings or Agents:

```sql
WITH candidate_counts AS (
  SELECT w.id,
         w.onboarding_agent_id,
         count(a.id) FILTER (
           WHERE a.owner_id = owner_member.user_id
             AND a.display_name IN ('Wendy', 'Windy', 'Joe')
         ) AS candidate_count
  FROM workspace w
  JOIN member owner_member
    ON owner_member.workspace_id = w.id
   AND owner_member.role = 'owner'
  LEFT JOIN agent a ON a.workspace_id = w.id
  GROUP BY w.id, w.onboarding_agent_id
)
SELECT CASE
         WHEN onboarding_agent_id IS NOT NULL THEN 'already_bound'
         WHEN candidate_count = 0 THEN 'no_candidate'
         WHEN candidate_count = 1 THEN 'one_candidate'
         ELSE 'multiple_candidates'
       END AS classification,
       count(*) AS workspace_count
FROM candidate_counts
GROUP BY classification
ORDER BY classification;
```

Migration 302 was the one-time data cut that bound only `one_candidate`
Workspaces. It did not update Agent rows, so Runtime and Model remained
unchanged. Current Setup does not repeat that name-based migration logic:
`Wendy` is only a default display name, and an unbound Workspace always creates
a new ordinary Agent before storing `workspace.onboarding_agent_id`.

After the historical migration deployment, rerun the query only as a data
audit. Name matches no longer drive runtime identity, authorization, or Setup.
