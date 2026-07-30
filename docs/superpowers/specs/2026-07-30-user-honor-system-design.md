# User honor system (levels, badges, name styles)

Status: design approved in product discussion; implementation plan TBD  
Date: 2026-07-30  
Audience: engineers, designers, operators

## 1. Summary

Add a **platform-global** user honor system modeled on mature forum / game / QQ-VIP
patterns: behavior earns XP, XP maps to **level**, level unlocks **name style**
tiers (auto-equipped highest), independent **achievements** unlock **badges**
(user picks one to wear). Early adopters (`user.created_at < 2026-08-01`) receive
**Founding** identity. Rules are **public**; XP ledgers are **private** to the
account owner.

Goal: distinguish veteran and founding users from ordinary users with visible
identity (colored / glowing / animated names + badge) without inventing a novel
gamification model.

## 2. Product decisions (locked)

| Topic | Decision |
| --- | --- |
| Scope | **Global** — same honor everywhere, attached to `user`, not `member` |
| Unlock paths | Automatic achievements + level thresholds + **ops manual grants** |
| Paid membership | **Not in v1**; schema reserves `membership_tier` / subscription fields |
| Name style equip | **Auto highest** unlocked tier; no manual downgrade |
| Badge equip | User **chooses one** among unlocked badges |
| Display surfaces | **Everywhere** a user name renders (channels, issues, mentions, lists, popovers, search) |
| vs Owner/Admin | **Coexist** — honor styling + worn badge + existing workspace role pills |
| Badge assets | **Built-in SVG icon library**; must look polished / “cool”, not generic dots |
| Public profile | Others see **level + worn badge + full unlocked badge gallery** (achievement wall) |
| Others do **not** see | Total XP, XP bar, pillar breakdown, +XP event log |
| Owner private page | Full XP, pillar tier progress, ledger, badge picker, style preview |
| Founding cutoff | `user.created_at < 2026-08-01T00:00:00Z` (auto achievement + badge + style floor) |
| Difficulty curve | Industry-standard **steep tier ladder** (see §5); not linear, not easy to max |
| Mechanism style | Copy **forum level + QQ-style diamonds / colorful nicknames**; do not invent new rules |

## 3. Non-goals (v1)

- Paid subscription perks (reserved only)
- Workspace-scoped honor or per-team leaderboards
- Replacing Owner/Admin authorization or hiding those role labels
- Showing level numbers inline on every message line (styles + badge only in feed)
- Custom user-uploaded badge images (ops catalog only in v1)
- Agent honor / XP (users only)

## 4. Visibility model

### 4.1 Others (any signed-in user)

- Resolved **name style** (highest unlocked)
- **Worn badge** (one)
- **Level** (e.g. `Lv.12`) on profile surfaces and member detail — not required on every inline name in feed
- **Achievement wall**: all unlocked badges (worn badge highlighted)
- Link to public **Honor rules** page (read-only)

### 4.2 Self only

Route: **`/:slug/settings/honor`** (Settings nav item **荣誉**; not workspace main nav)

Contents:

- Current level, total XP, progress to next level
- **Four pillar** tier progress (T1–T8) with thresholds
- Recent **+XP ledger** (auditable events)
- Unlocked name styles (read-only list; effective = highest)
- Badge gallery + **equip picker** (one active)
- Link to Honor rules

Entry points (low footprint):

- Settings → **荣誉** (primary)
- Settings → Account: one-line summary `Lv.N · 查看详情 →`
- Optional: avatar menu → **我的荣誉**
- Own profile popover: level + badges + “查看我的荣誉详情” link; no XP in popover

### 4.3 Honor rules page (public)

Static, versioned document (in-app help + linked from Settings):

- Pillar definitions: Usage, Presence, Delivery, Community
- Action → XP table per pillar
- Daily / per-action caps (anti-spam)
- Level threshold table
- Pillar tier T1–T8 thresholds per tag
- Name style ↔ level / achievement matrix
- Badge catalog and unlock conditions
- Founding cutoff date
- Changelog when rules version bumps

## 5. Progression design (industry-standard)

### 5.1 XP → level

- Valid product actions append rows to **`user_xp_ledger`** (immutable, auditable)
- **Total XP** = sum(ledger) subject to published caps
- **Level** = lookup on public threshold table (exponential-style curve: fast early, very slow at top)
- Reference: Discuz / forum level tables, QQ VIP level brackets — tune numbers in config, not code magic

### 5.2 Four pillars (each has T1–T8 tiers)

| Pillar | Tag | Measures (examples) |
| --- | --- | --- |
| Usage | `usage` | Issue create/update, channel messages, research/automation actions |
| Presence | `presence` | Active minutes (heartbeat + action-gated; AFK excluded) |
| Delivery | `delivery` | Issue close/complete, project progress, substantive review activity |
| Community | `community` | Invites, channel participation, collaboration signals (lower weight) |

**Tier difficulty template** (relative steepness, **not** literal calendar days):

`T1 → T2 → T4 → T8 → T16 → T30 → T60 → T100`

Use this as the **difficulty shape** when setting thresholds per pillar:

- T1–T2: onboarding feedback
- T3–T5: regular users
- T6–T7: veteran
- T8: cap / legendary (months+ of real use)

Publish exact thresholds per pillar in Honor rules config.

### 5.3 Name style tiers (auto highest)

Ordered by rarity (effective style = max unlocked):

| Style key | Visual (direction) | Typical unlock |
| --- | --- | --- |
| `default` | Normal foreground | Everyone |
| `member` | Red | Mid-low level or usage T2 |
| `gold` | Gold | Mid level or multi-pillar T4 |
| `prismatic` | Static rainbow gradient | High level |
| `glow` | Glow / outline | High level or rare achievement |
| `shimmer` | Shimmer sweep | Very high level |
| `animated_prismatic` | Animated gradient | Top level + high pillar tiers |
| `animated_glow` | Animated glow | Top level + special achievement |
| `founding` | Founding-exclusive treatment | `created_at < 2026-08-01`; **floor** with level max, not a free max tier |

Implementation: CSS token classes on display name (e.g. `honor-name--gold`). Respect `prefers-reduced-motion: reduce` → static fallback.

### 5.4 Badges

- Defined in **`honor_badge_def`** (id, name, description, svg_key, rarity, unlock_rule)
- Unlock via achievements (level, pillar tier, founding, ops grant, etc.)
- User **`equipped_badge_id`** — one at a time; user-selectable among unlocked
- Render: small SVG after display name; hover shows title + description
- Design bar: **visually distinctive** (forum/medal quality), shipped as static assets in `packages/ui`

### 5.5 Ops manual grants

- Operators can grant **badge** and/or **name style** unlocks by user id
- Grants append to **`user_honor_grant`** audit table (who, when, reason)
- **Must not** silently edit XP ledger (fairness); optional separate “honor grant” does not inflate level unless explicitly a documented rule
- Founding backfill: one-time job for `created_at < 2026-08-01`

### 5.6 Paid membership (reserved)

Schema fields only in v1:

- `membership_tier`, `membership_expires_at` on user honor snapshot
- No scoring or UI in v1

## 6. Rendering architecture

### 6.1 Single pipeline

All user name rendering goes through shared identity components:

- `packages/views/common/actor-identity-row.tsx` (primary)
- Mention tokens, comment cards, channel bubbles, member lists, search hits

Extend **`MemberWithUser` / profile API** with compact honor snapshot:

```typescript
honor?: {
  level: number;
  name_style: HonorNameStyleKey; // server-computed max unlocked
  equipped_badge?: { id: string; svg_key: string; title: string };
  // full badge list only on profile/honor endpoints, not inline
}
```

### 6.2 Inline vs profile

| Surface | Name style | Worn badge | Level text | Badge wall |
| --- | --- | --- | --- | --- |
| Message / comment inline | yes | yes | no | no |
| Member list | yes | yes | optional small `Lv.N` | no |
| Profile popover / detail | yes | yes | yes | yes (others) |
| Settings honor (self) | preview | picker | yes | yes + XP |

Workspace **Owner/Admin** pills render **after** honor badge per product decision A.

## 7. Data model (sketch)

Global tables (names indicative):

| Table | Purpose |
| --- | --- |
| `honor_name_style_def` | Style key, min level, min pillar rules, css token, sort rank |
| `honor_badge_def` | Badge metadata, svg_key, rarity, unlock_rule JSON |
| `honor_achievement_def` | Rule definitions (founding date, level, pillar tier, etc.) |
| `user_honor` | user_id, level, total_xp (cached), equipped_badge_id, membership fields (reserved) |
| `user_honor_unlock` | user_id, unlock_type (style/badge), def_id, granted_at, source (auto/ops) |
| `user_xp_ledger` | user_id, pillar, action_type, xp_delta, ref_id, created_at |
| `user_pillar_progress` | user_id, pillar, tier, progress counters (cached) |
| `user_honor_grant` | ops audit log |

Level and `name_style` on read path can be derived from unlocks + defs; cache on `user_honor` for hot paths.

## 8. API (sketch)

| Endpoint | Access | Returns |
| --- | --- | --- |
| `GET /api/honor/rules` | authenticated | Public rules document + version |
| `GET /api/me/honor` | self | Full honor dashboard (XP, ledger, pillars, unlocks, equip) |
| `PATCH /api/me/honor` | self | `{ equipped_badge_id }` only in v1 |
| `GET /api/users/{id}/honor` | authenticated | Public wall: level, badges, worn badge — **no XP** |
| Profile embed | authenticated | Compact `honor` on existing member profile responses |

XP writes: internal service only (action hooks + nightly reconciliation), not client-callable.

## 9. UI surfaces

| Surface | Package / route |
| --- | --- |
| Settings nav item 荣誉 | `packages/views/settings/` → `/:slug/settings/honor` |
| Account summary line | `account-tab.tsx` |
| Honor rules help | `packages/views/settings/` or `packages/views/help/` |
| Profile honor tab | `actor-profile-popover.tsx`, `member-detail-page.tsx` |
| Inline name + badge | `actor-identity-row.tsx` |
| Badge SVG components | `packages/ui/components/honor/` |

Web + Desktop: wire routes in both apps (same pattern as other settings pages). Mobile: follow dashboard module coverage when honor ships.

## 10. Fairness & anti-abuse

- Published caps on repetitive actions per day
- Presence requires activity signals, not idle tabs
- Ledger retained for dispute/debug
- Rule changes bump `rules_version` and appear in changelog
- Founding is registration-time based, one-time

## 11. Testing & acceptance

- Go: ledger append, level computation, founding backfill, ops grant audit, equip validation
- FE: malformed honor payload parsing (`parseWithFallback`), `ActorIdentityRow` styles, reduced-motion fallback
- E2E smoke: founding user shows founding badge; self honor page shows XP; other profile hides XP

## 12. Implementation sequencing (recommended)

1. Schema + defs seed + founding backfill script
2. XP ingestion hooks (minimal action set) + level calculator
3. Profile honor embed + `ActorIdentityRow` rendering
4. Settings honor page (self) + public rules page
5. Badge SVG set + equip API
6. Ops grant tooling
7. Name style high tiers (animated) after base path stable

See implementation plan (to be written): `docs/superpowers/plans/2026-07-30-user-honor-system.md`.

## 13. Discussion log (why this doc exists)

Product thread established:

- Forum / QQ-style familiarity over custom gamification
- Global identity for founding and veteran recognition
- Steep tier curve (1/2/4/8/16/30/60/100 difficulty shape)
- Transparency via public rules; privacy for XP details on Settings honor page only
- Do not occupy workspace main navigation
