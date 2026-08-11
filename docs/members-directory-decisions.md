# Members Directory — product decisions (2026-08-11)

Grilled with product owner. ADR: [`docs/adr/0013-members-directory-replaces-agents-page.md`](./adr/0013-members-directory-replaces-agents-page.md).

This file is the decision table for implementation. It is not a task plan.

## Settled decisions

| # | Topic | Decision |
|---|--------|----------|
| 1 | Success shape | Unified **Members Directory**: left roster + right profile; replaces Agents entry |
| 2 | Settings Members tab | **Delete entire tab** (invite, role, remove, pending UI). Sole human-admin entry = Directory |
| 3 | Human manage UI | Right **Member Profile**: **edit Role** + **Remove Member** (owner/admin rules unchanged) |
| 4 | Pending invites | **v1: no** list / revoke / resend |
| 5 | Product naming | User-visible **Members** only — no dual Agents/Members product vocabulary |
| 6 | FE route | **`/:slug/members`**; sidebar **Members**; `/agents`, `/agents/:id` **redirect** to directory / selection |
| 7 | API | Under **`members`** prefix (implementer designs paths); **core** uses new paths; keep **`/api/agents` alias** for mobile/CLI |
| 8 | Desktop / mobile | **Web UI in scope**; desktop not special-cased (shared packages); **mobile UI out**; mobile may keep old API via alias |
| 9 | Graph | **v1: no** |
| 10 | Old Agents pages | **Delete** list page + `AgentDetailPage`; right pane = existing **Side Panels** only |
| 11 | AGENTS grouping | By **computer** via same **`buildRuntimeMachines`** as Runtimes; **no “No computer” group**; create requires computer; agents without runtime **omitted** from the rail |
| 12 | HUMANS `+` | **Invite dialog**: single email + role (`createMember`); no multi-email, no join link |
| 13 | AGENTS `+` | Existing **CreateAgentDialog** |
| 14 | Archived agents | **Not** in left rail |
| 15 | Default selection | First agent under first computer, else first human |
| 16 | Hire/draft deep links | **Drop** (`?action_card=`, `?draft=` auto-create) |
| 17 | Agent detail deep links | `/agents/:id` → directory with that agent selected |

## Layout (desktop) — page draft (PD)

```
Members
▾ AGENTS  n                    [+]  → CreateAgentDialog (computer required)
  💻 <machine title>             (header only: not clickable, not collapsible)
     · avatar + name [+ optional truncated description]
  💻 …
▾ HUMANS  m                    [+]  → Invite (owner/admin only; single email + role)
  · avatar + name [(you)]
  · …
```

Page draft locks (2026-08-11):

| PD | Decision |
|----|----------|
| PD1 | No “No computer” group; computer **required** at agent create |
| PD2 | Computer rows = **labels only** (no click, no collapse) |
| PD3 | **No** left-rail search in v1 |
| PD4 | Selection in **URL query** (share/refresh) |
| PD5 | Row = **avatar + display name**; agent may show truncated description subtitle; human may show `(you)` |
| PD6 | HUMANS `+` visible to **owner/admin only** |
| PD7 | Profile page variant **hides ✕/Done**; change selection via left rail |

Right:

- Agent selected → `ResolvedAgentSidePanel` / `AgentSidePanel` (`variant="page"`); loading/403/error must terminate (no infinite Loading)
- Human selected → `MemberSidePanel` (`variant="page"`) + role edit + remove

Mobile web: list ↔ detail shell (`MobileListDetailLayout` or equivalent).

## Docs to update when implementing

- `apps/docs/content/docs/members-roles.mdx` — invite/admin entry = Members Directory, not Settings
- Sidebar / layout i18n (Agents → Members)
- `packages/core/paths` — `members()`, selection helpers; deprecate `agents()` / `agentDetail()` as redirects only
- Any skill or runbook that tells users to open Settings → Members or the Agents page for roster admin

## Implementation notes (non-binding)

- Prefer reusing panel components; extend human panel for role/remove rather than rebuilding Profile.
- Computer grouping must not invent a second machine identity; reuse Runtimes machine builder + desktop `localDaemonId` props if the page is shared.
- API path table is intentionally not frozen here — hang under `members`, dual-run old `/api/agents` until mobile/CLI migrate.
