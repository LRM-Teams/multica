-- Deduplicate concurrent seed races (same fleet_id + role), then enforce uniqueness
-- for non-archived members so ensureResearchFleet cannot mint two 罗纳尔多 / scouts.

WITH ranked AS (
  SELECT
    id,
    ROW_NUMBER() OVER (
      PARTITION BY fleet_id, role
      ORDER BY
        CASE WHEN status = 'archived' THEN 1 ELSE 0 END,
        is_lead DESC,
        created_at ASC,
        id ASC
    ) AS rn
  FROM research_fleet_member
)
UPDATE research_fleet_member m
SET status = 'archived', updated_at = now()
FROM ranked r
WHERE m.id = r.id
  AND r.rn > 1
  AND m.status <> 'archived';

CREATE UNIQUE INDEX IF NOT EXISTS research_fleet_member_fleet_role_active_uidx
  ON research_fleet_member (fleet_id, role)
  WHERE status <> 'archived';
