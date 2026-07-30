# Agent fleet rank (warship class system)

Status: approved (archived freeze + unified pool)  
Date: 2026-07-30

## Goal

At a glance in a workspace (agents list, profile, avatar), answer:

- **Who is the strongest agent in this fleet?**
- Who is solid / average / still warming up?

Mechanics are **data-driven** (delivery + evolution + growth + efficiency); skin is **宇宙战舰** IP.

## Scope

| Dimension | Decision |
| --- | --- |
| Bound | **Workspace-scoped** — agents compete within their fleet |
| vs Memory Growth | **Coexist** — Growth = learning XP; Fleet Rank = operational combat rating |
| Cold start | New / low-sample agents show **Reserve** with explicit sample count — never fake S-tier |
| Archived agents | **Frozen** — retain last computed class/rank at archive; excluded from active recompute |
| Agent pool | **All non-archived agents** rank together (user + managed/platform agents) |

## Scoring (30-day window)

| Pillar | Weight | Sources |
| --- | --- | --- |
| Delivery | 55% | `agent_inbox_event` terminal outcomes (Bayesian success + volume) |
| Evolution | 25% | `evolution_unit_feedback_event` + promoted `evolution_unit_submission` |
| Growth | 15% | Memory Growth tier + 30d write velocity |
| Efficiency | 10% | Tokens + duration per completed task |

**Sample gate:** fewer than 5 terminal tasks → class locked to **Reserve**.

## Classes

| Class | Min score |
| --- | --- |
| Dreadnought | 85 |
| Battleship | 70 |
| Cruiser | 55 |
| Frigate | 40 |
| Corvette | 25 |
| Reserve | — (or insufficient sample) |

Workspace-relative `#1 #2 #3` ranks drive Top-3 pennant on avatars.

## API

- `GET /api/agents/fleet-rankings`
- `GET /api/agents/fleet-rank/rules`
- `GET /api/agents/{id}/fleet-rank`

## Archive freeze

On `POST /api/agents/{id}/archive`:

1. Recompute workspace rankings
2. Freeze archived agent snapshot
3. Recompute remaining active agents

Frozen rows are never overwritten by upsert.
