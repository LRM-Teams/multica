# User honor system (levels, badges, name styles)

Status: implemented
Date: 2026-07-30
Last updated: 2026-08-04 (reachable 80-level progression)

## Product summary

Global forum/QQ-style honor mechanics with a **sci-fi cosmic IP** skin:

- Behavior → XP → level; four pillars with steep T1–T8 tiers
- Badges unlocked by achievements; user equips **one**; name style = **auto highest**
- Founding: `user.created_at < 2026-08-01T00:00:00Z`
- Public rules; XP details private on Settings → **荣誉** (`?tab=honor`)
- Owner/Admin role pills coexist

The catalog now contains 24 visible name tiers and 51 badges. New definitions
remain data-driven so later catalog additions do not change the XP ledger.

## Level progression

Levels 1-20 retain the original onboarding thresholds. Levels 21-80 use four
piecewise-linear increment bands so the tail remains progressively harder
without exponential runaway. The current milestones are 874 XP at level 20,
7,474 at level 40, 31,774 at level 60, 68,024 at level 70, and 140,524 at
level 80. Every new threshold is less than or equal to the previous rule, so a
rules deployment cannot demote an existing user.

The server publishes all 80 cumulative thresholds through the rules API.
Clients consume that table and do not reproduce the formula. Migration 283
recalculates stored levels and grants newly reached level-gated styles and
badges during deployment.

---

## Cosmic IP

**Setting:** users progress through a “collaboration universe” — inner planets → outer planets → stellar classes → phenomena badges.

| Layer | Content |
| --- | --- |
| **Name progression** | 24 visible tiers from Default to Transcendent |
| **Badge catalog** | 51 level, pillar, founding, stellar, and phenomenon badges |
| **Late game** | Secret badges from Quantum Gate through Infinity Engine |

Founding users get **Genesis Nebula** identity; does not auto-grant max stellar tier.

---

## Visual quality bar (badges)

**Not allowed:** flat 16px doodle icons.

**Required:**

- Layered art: ring + core + highlight + optional particle layer
- Source at 32/48px; scales down without losing detail
- Sci-fi material language (metal, energy core, orbit lines)
- Motion via CSS glow/pulse/shimmer on separate layers — not GIF stickers

---

## Glow tiers (luminosity by level)

Seven tiers (**I–VII**). Higher tier = larger halo, more layers, slightly faster pulse — **never strobe**.

| Tier | Typical level | Inline (messages) | Profile / honor page |
| --- | --- | --- | --- |
| **I** None | Lv1–5 | Plain | Plain |
| **II** Micro | Lv6–12 | Soft edge | Soft edge |
| **III** Steady | Lv13–22 | Slow breathe (3–4s) | Same |
| **IV** Pulse | Lv23–35 | Static color + breathing halo | Pulse + sparkles |
| **V** Sweep | Lv36–45 | Static gradient + breathing halo | Shimmer sweep |
| **VI** Nebula | Lv46+ | Capped to V | Flowing gradient |
| **VII** Legend | Lv50+ | Capped to V | Full multi-layer sync |

### Anti-harsh rules (approved)

1. **Message/list surfaces cap at tier V**; text-gradient motion is disabled there,
   while the slow halo may breathe.
2. Glow on **halos only**; text keeps readable contrast.
3. Pulse period **≥ 2.5s**; no sub-second flashing.
4. Halo opacity cap ~**0.35–0.45**; avoid large pure-white flashes.
5. **`prefers-reduced-motion: reduce`** → static high tier (color/metal only).
6. Future optional: Settings honor motion = standard / soft / off.

---

## Architecture

- **Global** `user_honor*` tables + seed defs (`honor_badge_def`, `honor_name_style_def`)
- **HonorService** — XP ledger, pillar tiers, founding backfill, level/style reconciliation
- **HTTP** — `GET /api/honor/rules`, `GET/PATCH /api/me/honor`, `POST /api/me/honor/presence`, `GET /api/users/{id}/honor`
- **Embed** — compact `honor` on member list + user member profile
- **UI** — `ActorIdentityRow`, channel author row, full-width Settings **荣誉**
  tab, member side-panel wall, and avatar/name profile popover showcase

CSS: `data-honor-glow-tier` + tokens (`--honor-glow-color`, `--honor-pulse-duration`).

---

## Implementation status

- Migration `251_user_honor`
- XP hooks: issue create/close, comment, channel message, presence endpoint
- Four pillars + public rules API
- Founding auto-unlock
- 51 code-native cosmic badge SVGs, including 34 expanded catalog designs
- 24 readable light/dark name styles + seven glow tiers + reduced-motion fallback
- Xbox-style locked/in-progress/unlocked catalog, rarity, secret achievements,
  equip, three-slot showcase, comparison, recent unlocks, and realtime toast
- Dense member/DM lists suppress badge icons; profile/message surfaces retain
  intentional honor presentation
- Avatar/name hover cards fetch the public honor wall and display level, equipped
  crest, showcase, and collection progress

## Reserved follow-up

- Paid `membership_tier` remains schema-only and has no scoring effect.
