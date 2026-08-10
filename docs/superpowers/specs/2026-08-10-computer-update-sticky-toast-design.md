# Computer update sticky toast

Date: 2026-08-10  
Status: approved for implementation (MVP)

## Problem

When a local computer (daemon) has a CLI update available, users often miss it.
The sidebar amber attention icon next to Computers was easy to ignore; Inbox is
not the right surface ("nobody looks at Inbox"). A full global notification
bell is out of scope for this MVP.

## Decision

Ship **one sticky toast per updatable machine**, with in-toast actions.
Remove the Computers sidebar amber `RuntimeAttentionAlert`.

## Product rules

1. **One toast per machine** (`daemon_id`, fallback `runtime:{id}`).
2. **Sticky only for computer-update toasts** (`duration: Infinity`). All other
   product toasts keep auto-dismiss defaults — do not change sonner globally.
3. **Actions**
   - **Update now** → `POST /api/daemons/:daemonId/upgrades` via
     `api.initiateMachineUpgrade` (same path as Computers detail); poll
     `getMachineUpgrade` until terminal; toast switches to updating /
     success / failed.
   - **Later** → dismiss toast and remember dismiss for
     `(workspaceId, machineKey, targetVersion)` in `localStorage`.
4. **Re-show** when `targetVersion` changes (newer release) even if previously
   dismissed for an older target.
5. **Eligibility** matches existing self-update gates:
   owner = me, local, online, not sandbox, not desktop-managed, not mid-lifecycle
   (`pending` / `running` / `ready_to_apply`), `runtime_health === update_available`,
   target newer than current.
6. **Surfaces**
   - Sticky toast: primary proactive prompt.
   - Computers detail page upgrade UI: still the detailed progress/error surface.
   - No Inbox item. No global notification bell (future optional).
   - **Delete** Computers amber attention icon.

## State machine (per machine toast id `computer-update:{machineKey}`)

| Phase | Sticky? | UI |
|-------|---------|-----|
| prompt | yes | title, current → target, Update / Later |
| updating | yes | progress copy (pending / running / ready_to_apply) |
| success | no (~4s) | short success then auto-dismiss |
| failed | yes | error line + Retry / Later |

Same toast id morphs across phases (sonner replace-by-id).

## Dismiss storage

- Key: `multica:computer-update-dismiss:{wsId}:{machineKey}`
- Value: dismissed `targetVersion` string
- Client-only (`localStorage`); no server preference API

## Out of scope

- Global notification center / bell panel
- Inbox `InboxItemType` for daemon updates
- Aggregated multi-machine single toast
- Mobile

## Implementation sketch

- Pure helpers in `@multica/core/runtimes` (candidates + dismiss key helpers)
- `ComputerUpdateToast` + `ComputerUpdateToastListener` in `@multica/views`
- Mount listener in `DashboardLayout` (web + desktop share it)
- Remove `RuntimeAttentionAlert` mount and component
