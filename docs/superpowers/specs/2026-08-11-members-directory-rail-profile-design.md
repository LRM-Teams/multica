# Members Directory rail + human profile UI alignment

**Status:** approved (draft HTML + Frank review)  
**Draft:** `artifacts/members-directory-draft.html`  
**Date:** 2026-08-11

## Goal

Tighten Members Directory left rail and make the human detail panel **visually match** the agent profile shell — style only, not agent feature parity (no Activity/Reminders/Files/Usage tabs on humans).

## Left rail

| Decision | Detail |
|----------|--------|
| Search | Filters agent name/description/machine title + human name/email |
| Collapse | Agents **and** Humans collapsible |
| Defaults | Agents **collapsed**, Humans **expanded** |
| Collapse chrome | Header row only (label + count + chevron + add); **no** “collapsed…” hint |
| Auto-expand | Selecting an agent/human expands that section |
| Sort humans | **Current user first**, then display name |
| Sort agents | **Mine first** (`owner_id === currentUser`), then name (within computer group) |
| Mine badge | “Mine” / i18n pill on rows the current user owns |
| All / Mine filter | **Not shipped** — own agents live under self profile → Created agents |

## Human profile (vs agent)

Shared skeleton:

- Identity block (avatar + name + handle) as header content  
- When embedded in Directory (`hideDismiss`): **floating** shell (no name chrome bar), like agent  
- Sections: Display name / Description → Honor → Info → Created agents → **Actions**  
- Info grid: `100px` label column, 13px body (match agent)  
- Role: **single** place in Info; pencil opens `RolesDialog` when allowed  
- Actions: vertical stack — Message (outline) + Remove (destructive), match `AgentProfileActions` rhythm  

Not on human:

- Tabs (Profile/Activity/…)  
- Runtime config / Restart / Delete agent  

## Manage wiring

- Remove bolted `MemberDirectoryManageFooter` (dual Role + bottom Remove).  
- Directory page passes manage callbacks into `MemberSidePanel` when viewer is workspace owner/admin.  
- Conversation dock usage of `MemberSidePanel` unchanged except visual polish (still Message affordance in Actions or dock as appropriate).

## Out of scope

- Changing agent profile tabs  
- Settings → Members list redesign  
- Persisting collapse state across reloads  
