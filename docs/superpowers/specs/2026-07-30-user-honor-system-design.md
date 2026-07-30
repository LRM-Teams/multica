# User honor system (levels, badges, name styles)

Status: approved; implementation in progress  
Date: 2026-07-30  
Last updated: 2026-07-30 (cosmic IP + glow safety)

## Product summary

Global forum/QQ-style honor mechanics with a **sci-fi cosmic IP** skin (phased content rollout):

- Behavior → XP → level; four pillars with steep T1–T8 tiers
- Badges unlocked by achievements; user equips **one**; name style = **auto highest**
- Founding: `user.created_at < 2026-08-01T00:00:00Z`
- Public rules; XP details private on Settings → **荣誉** (`?tab=honor`)
- Owner/Admin role pills coexist

Mechanics follow industry patterns; **IP assets expand over time** without changing core rules.

---

## Cosmic IP (content roadmap)

**Setting:** users progress through a “collaboration universe” — inner planets → outer planets → stellar classes → phenomena badges.

| Phase | Content |
| --- | --- |
| **Now** | Core mechanics + placeholder badges (replace before ship) |
| **Next** | 12–16 **ship-badge quality** SVGs: 9 planets + founding nebula + 3 stellar tiers |
| **Later** | 40+ phenomenon/constellation badges; stellar rank titles (red dwarf → quasar) |

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
| **IV** Pulse | Lv23–35 | **Not inline** | Pulse + sparkles |
| **V** Sweep | Lv36–45 | **Not inline** | Shimmer sweep |
| **VI** Nebula | Lv46+ | **Not inline** | Flowing gradient |
| **VII** Legend | Founding / stellar cap | **Not inline** | Full multi-layer sync |

### Anti-harsh rules (approved)

1. **Message/list surfaces cap at tier III** — no sweep or fast flash in feed.
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
- **UI** — `ActorIdentityRow`, channel author row, Settings **荣誉** tab

CSS: `data-honor-glow-tier` + tokens (`--honor-glow-color`, `--honor-pulse-duration`).

---

## v1 implementation status

- Migration `251_user_honor`
- XP hooks: issue create/close, comment, channel message, presence endpoint
- Four pillars + public rules API
- Founding auto-unlock
- **Placeholder** badge SVGs (must replace per quality bar above)
- Basic name CSS tokens + reduced-motion fallback

## Follow-ups

- Replace placeholders with cosmic badge set (batch 1: 12–16)
- Map level → glow tier III cap in inline renderers
- Ops manual grant API
- CoreProvider presence heartbeat
- Profile popover honor wall
- Public rules help page
- Paid `membership_tier` (schema reserved)
