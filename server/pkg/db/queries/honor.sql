-- name: GetUserHonor :one
SELECT * FROM user_honor WHERE user_id = $1;

-- name: SumUserXpLedger :one
SELECT COALESCE(SUM(xp_delta), 0)::bigint AS total
FROM user_xp_ledger
WHERE user_id = $1;

-- name: UpdateUserHonorStats :one
UPDATE user_honor
SET total_xp = $2, level = $3, updated_at = now()
WHERE user_id = $1
RETURNING *;

-- name: UpsertUserHonor :one
INSERT INTO user_honor (user_id, total_xp, level, equipped_badge_id, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (user_id) DO UPDATE SET
    total_xp = EXCLUDED.total_xp,
    level = EXCLUDED.level,
    equipped_badge_id = COALESCE(user_honor.equipped_badge_id, EXCLUDED.equipped_badge_id),
    updated_at = now()
RETURNING *;

-- name: CreateUserHonorIfMissing :one
INSERT INTO user_honor (user_id)
VALUES ($1)
ON CONFLICT (user_id) DO UPDATE SET updated_at = user_honor.updated_at
RETURNING *;

-- name: UpdateUserHonorEquippedBadge :one
UPDATE user_honor
SET equipped_badge_id = $2, equipped_badge_manual = $3, updated_at = now()
WHERE user_id = $1
RETURNING *;

-- name: ListHonorBadgeDefs :many
SELECT * FROM honor_badge_def ORDER BY sort_rank DESC;

-- name: GetHonorBadgeDef :one
SELECT * FROM honor_badge_def WHERE id = $1;

-- name: ListHonorNameStyleDefs :many
SELECT * FROM honor_name_style_def ORDER BY sort_rank ASC;

-- name: ListUserHonorUnlocks :many
SELECT * FROM user_honor_unlock
WHERE user_id = $1
ORDER BY granted_at ASC;

-- name: InsertUserHonorUnlock :one
INSERT INTO user_honor_unlock (user_id, unlock_kind, def_id, source)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, unlock_kind, def_id) DO UPDATE SET granted_at = user_honor_unlock.granted_at
RETURNING *;

-- name: InsertUserXpLedger :one
INSERT INTO user_xp_ledger (user_id, pillar, action_type, xp_delta, ref_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: SumUserXpLedgerTodayByAction :one
SELECT COALESCE(SUM(xp_delta), 0)::bigint AS total
FROM user_xp_ledger
WHERE user_id = $1
  AND action_type = $2
  AND created_at >= date_trunc('day', now() AT TIME ZONE 'UTC');

-- name: ListUserXpLedgerRecent :many
SELECT * FROM user_xp_ledger
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: GetUserPillarProgress :one
SELECT * FROM user_pillar_progress
WHERE user_id = $1 AND pillar = $2;

-- name: UpsertUserPillarProgress :one
INSERT INTO user_pillar_progress (user_id, pillar, counter_value, tier, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (user_id, pillar) DO UPDATE SET
    counter_value = EXCLUDED.counter_value,
    tier = EXCLUDED.tier,
    updated_at = now()
RETURNING *;

-- name: ListUserPillarProgress :many
SELECT * FROM user_pillar_progress
WHERE user_id = $1
ORDER BY pillar ASC;

-- name: ListUsersBeforeFoundingCutoff :many
SELECT id, created_at FROM "user"
WHERE created_at < $1::timestamptz;

-- name: ListUserHonorByUserIDs :many
SELECT * FROM user_honor
WHERE user_id = ANY($1::uuid[]);

-- name: ListUserHonorUnlocksByUserIDs :many
SELECT u.* FROM user_honor_unlock u
WHERE u.user_id = ANY($1::uuid[]);

-- name: InsertUserHonorUnlockIfNew :one
INSERT INTO user_honor_unlock (user_id, unlock_kind, def_id, source)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id, unlock_kind, def_id) DO NOTHING
RETURNING *;

-- name: CountHonorBadgeUnlocks :many
SELECT def_id, COUNT(*)::bigint AS unlock_count
FROM user_honor_unlock
WHERE unlock_kind = 'badge'
GROUP BY def_id;

-- name: CountHonorUsers :one
SELECT COUNT(*)::bigint AS total FROM user_honor;

-- name: UpdateUserHonorShowcase :one
UPDATE user_honor
SET showcase_badge_ids = $2, updated_at = now()
WHERE user_id = $1
RETURNING *;

-- name: ListRecentBadgeUnlocks :many
SELECT u.def_id, u.granted_at, d.title, d.description, d.svg_key
FROM user_honor_unlock u
JOIN honor_badge_def d ON d.id = u.def_id
WHERE u.user_id = $1 AND u.unlock_kind = 'badge'
ORDER BY u.granted_at DESC
LIMIT $2;
