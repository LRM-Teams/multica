# Agent honor system v2

Status: implemented
Date: 2026-07-31

## Product contract

Agent honor has two independent time horizons:

- **Permanent honor:** accepted deliveries and achievements write idempotent XP
  ledger entries. XP, level, unlocks, equipped achievement, and three-slot
  showcase do not expire.
- **Fleet rating:** workspace-relative operational rating over a configurable
  rolling window. It may rise or fall and records material score, class, or rank
  changes in history.

The separation prevents a 30-day ranking window from erasing long-term agent
progress.

## Level progression

Permanent agent honor has 30 levels. Level `n` starts at
`25 × (n - 1)²` lifetime XP; level 30 is terminal and reports no next-level
progress. The client derives a level crest from the numeric level using the 30
assets in `packages/views/agents/components/assets/honor-levels/`; achievements
and fleet classes remain independent systems.

## Achievement catalog

The initial catalog has 16 achievements across delivery, reliability, memory
growth, evolution, project breadth, recovery, and fleet class. Secret
achievements hide their title, rule, and art until unlocked.

Each achievement has:

- stable ID, metric, target, XP reward, rarity, category, and SVG key;
- progress when locked and global unlock percentage when visible;
- idempotent automatic or manual unlock;
- realtime owner-only unlock notification.

## Administration

Workspace owners/admins can configure:

- XP per accepted delivery;
- fleet window and minimum sample size;
- four fleet pillar weights;
- six strictly increasing class thresholds;
- per-achievement enable flags and targets.

Manual XP/achievement grants require a reason and write an audit row. Rules,
grants, and revocations are workspace-scoped.

## Data and APIs

Migration `255_agent_honor_system` adds:

- `agent_honor_state`, `agent_honor_event`, `agent_honor_unlock`;
- `agent_honor_rule_config`, `agent_honor_admin_audit`;
- `agent_fleet_history`;
- creation/backfill for existing agents and accepted deliveries.

Endpoints:

- `GET/PUT /api/agents/honor/rules`
- `GET /api/agents/honor/audit`
- `GET/PATCH /api/agents/{id}/honor`
- `POST /api/agents/{id}/honor/grants`
- `DELETE /api/agents/{id}/honor/achievements/{achievementId}`

Realtime:

- `agent_honor:achievement_unlocked`
- `agent_honor:level_changed`
- `agent_honor:fleet_class_changed`

Every event includes the Agent display name. Level changes invalidate the
Agent directory, detail, and honor queries so identity crests update on every
surface without a page reload. Promotion notifications name the Agent; level
decreases still invalidate cached identity data but do not show a promotion.

## UI surfaces

- Agent detail **Honor** tab: permanent XP, level progress, equipped crest,
  three-slot showcase, nearest targets, complete achievement catalog, event
  ledger, fleet rating/history, and admin dialog.
- Avatar/name hover profile: level, permanent XP, fleet class/rank, equipped
  achievement, and showcase.
- Global listener: custom achievement toast and fleet promotion toast with
  React Query invalidation.
