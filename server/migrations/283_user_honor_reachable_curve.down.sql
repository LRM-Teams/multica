-- Restore the previous exponential thresholds. Unlocks remain permanent: a
-- rollback must not destructively revoke badges or styles already awarded.
WITH RECURSIVE honor_thresholds(level, total_xp) AS (
    VALUES (1, 0::bigint)
    UNION ALL
    SELECT
        level + 1,
        total_xp + FLOOR(10 * POWER(1.15, level - 1))::bigint
    FROM honor_thresholds
    WHERE level < 80
), recalculated AS (
    SELECT user_honor.user_id, MAX(honor_thresholds.level)::int AS level
    FROM user_honor
    JOIN honor_thresholds ON honor_thresholds.total_xp <= user_honor.total_xp
    GROUP BY user_honor.user_id
)
UPDATE user_honor
SET level = recalculated.level,
    updated_at = now()
FROM recalculated
WHERE user_honor.user_id = recalculated.user_id
  AND user_honor.level IS DISTINCT FROM recalculated.level;
