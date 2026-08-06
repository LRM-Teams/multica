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

Migration 302 binds only `one_candidate` Workspaces. It does not update Agent
rows, so Runtime and Model remain unchanged. `already_bound`, `no_candidate`,
and `multiple_candidates` Workspaces are untouched. Owners of `no_candidate`
Workspaces complete the explicit Wendy Setup screen. Resolve every
`multiple_candidates` Workspace manually before retrying Setup; the API returns
`409 onboarding_agent_ambiguous` and does not choose, merge, archive, or delete.

After deployment, rerun the query. `one_candidate` should be zero. A nonzero
`multiple_candidates` count is an explicit repair queue, not a migration error
to work around by guessing.
