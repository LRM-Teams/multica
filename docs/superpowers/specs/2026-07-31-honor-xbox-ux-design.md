# Honor system — Xbox-inspired UX extension

Status: approved  
Date: 2026-07-31  

## Summary

Extends the platform honor system with a **complete Xbox Achievement-style product surface**:

- Unlock moment (WS + toast)
- Full badge catalog (locked / in-progress / unlocked + completion %)
- Profile showcase (up to 3 badges) separate from chat equip
- Global unlock rarity (% of users)
- Secret badges (hidden until unlocked)
- Member honor wall + compare with viewer

Auto-equip best badge (253) remains for inline chat; showcase is profile-only.

## Data model (254)

- `honor_badge_def.secret`, `honor_badge_def.unlock_rule`
- `user_honor.showcase_badge_ids` (max 3, app-enforced)
- `user_honor.equipped_badge_manual` (253)

## API

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/me/honor` | Dashboard + `badge_catalog`, completion, showcase, recent unlocks |
| PATCH | `/api/me/honor` | `equipped_badge_id` and/or `showcase_badge_ids` |
| GET | `/api/me/honor/compare?with={userId}` | Shared / exclusive badges vs another user |
| GET | `/api/users/{id}/honor` | Public wall + showcase + recent |

## Realtime

- `honor:badge_unlocked` → recipient user only; frontend toast + cache invalidation

## UI surfaces

- **Settings → Honor**: Xbox achievement list + equip/showcase actions
- **Member side panel**: Honor wall + compare (non-self)
- **Chat inline**: unchanged (`ActorStyledName` + auto best badge)

## Secret badges (seed)

`blue_giant`, `quasar`, `red_dwarf` — catalog shows placeholder until unlocked.
