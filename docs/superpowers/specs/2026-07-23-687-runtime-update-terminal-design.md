# #687 — Runtime update: make `ready_to_apply` terminal + intermediate state visible

Date: 2026-07-23
Owner: @Wren (FE) · Direction: @Barry · Product acceptance: @Parker
Branch: `fix/687-runtime-update-terminal` (off `dev`)

## Problem (the "black window")

Frank clicked **Update now** in the runtime-update prompt tonight and got no
feedback — he assumed it never triggered. The update actually ran; the UI just
never reflected it.

## Locked code facts

- `api.getUpdateResult` returns `RuntimeUpdate.status: RuntimeUpdateStatus`, and
  that enum **already includes `ready_to_apply`** (`core/types/agent.ts:796`).
- The prompt dialog poll (`runtime-update-dialog.tsx:189-209`) only treats
  `completed` / `failed` / `timeout` as terminal. `ready_to_apply` (and every
  intermediate) falls into the `else` "keep polling" branch → the poll **never
  stops, never refreshes runtimes, never shows a terminal UI**.
- The dialog only renders terminal UI for `completed` (287) and `failed/timeout`
  (292). There is **no `ready_to_apply` branch and no labeled intermediate**
  (just an unlabeled spinner via `isActive`).
- The single source of truth is the **runtime object**:
  `update_state: RuntimeUpdateState` (`idle|pending|running|completed|ready_to_apply|failed|timed_out`)
  + `runtime_health: RuntimeHealthState` (`ok|update_available|updating|failed|offline`)
  + `current_version` / `target_version` / `update_error`.

## Audit — who already consumes the single contract

All other consumers are **already correct** (derive from the runtime contract via
shared helpers); the dialog is the sole divergent one:

| Surface | File | Derivation | Status |
|---|---|---|---|
| Runtimes row | `runtime-columns.tsx` | `deriveRuntimeHealth` + `runtimeHealthState` | ✅ |
| Runtimes detail | `runtime-detail.tsx` → `update-section.tsx` | `updateState` + `runtimeHealth` props | ✅ (reference impl) |
| Agent runtime row | `agent-profile-card.tsx` | `runtimeHealthState` + `useRuntimeHealthStateLabel` | ✅ |
| Channel sidebar | `agent-side-panel.tsx` | `runtimeHealthState` + `useRuntimeHealthStateLabel` | ✅ |
| Update prompt dialog | `runtime-update-dialog.tsx` | **poll only, mishandles `ready_to_apply`, no intermediate** | ❌ fix |

`update-section.tsx` is the canonical good pattern: its poll treats
`ready_to_apply` as terminal (167-171), AND it derives a display status from the
contract (`statusFromUpdateState(updateState)` + `runtimeHealth`, 200-213) so the
daemon's downloading/staged state shows even without an active poll.

## Design

**Don't duplicate the state machine (Barry's constraint). Extract it once and
have both surfaces consume it.**

1. **Extract shared, pure derivation** (no React, no icons) into
   `packages/core/runtimes/update-status.ts`:
   - `statusFromUpdateState(state?: RuntimeUpdateState): RuntimeUpdateStatus | null`
   - `deriveUpdateStatus({ pollStatus, updateState, runtimeHealth }): RuntimeUpdateStatus | null`
     — the 200-213 mapping, lifted verbatim.
   - `isTerminalUpdateStatus(s): boolean` → `completed | ready_to_apply | failed | timeout`.
   - `UPDATE_TERMINAL_STATUSES` set.
   These are unit-testable in `@multica/core` with zero UI.

2. **`update-section.tsx`** — refactor to import the shared helpers instead of its
   local `statusFromUpdateState` + inline `derivedStatus` + terminal checks.
   **No behavior change** (snapshot/label tests stay green). The lucide
   `statusConfig` (icon+color) stays in views.

3. **`runtime-update-dialog.tsx`** — the actual fix (**natural handoff**, per
   Barry + Parker: "visible" ≠ "lock the user"; the drain can take hours, so the
   prompt must NOT pin a modal over it):
   - Poll stops on `isTerminalUpdateStatus(result.status)` (so `ready_to_apply`
     and `completed` both terminate) and then `refreshRuntimes()`.
   - On **initiate success**: `refreshRuntimes()` immediately (do NOT remember a
     dismissed key). The projection flips to `updating`, which drops the runtime
     from `runtimeCanStartSelfUpdate` eligibility, so the **prompt self-dismisses**
     and the global surfaces (AppShell / sidebar / Runtimes detail) take over
     showing progress. The local `pending` state only covers the brief pre-refresh
     window — long enough to prove the click registered, never a pinned modal.
   - The hidden poll keeps running to a terminal status purely for **stop +
     refresh**; `ready_to_apply` is shown on the **global surfaces** (Runtimes
     detail's `UpdateSection` reads `update_state`), NOT pinned in the prompt.
   - `deriveUpdateStatus` is the shared display projection for the brief in-prompt
     feedback and keeps the dialog consistent with `UpdateSection`.
   - `Later` is the ONLY user action that writes a dismissed key. A synchronous
     `initiateUpdate` failure (no handoff happened) shows the error + Retry in the
     still-open prompt; poll-driven failures surface on the global health state.
   - a11y: the brief feedback lives in an `<output>` (implicit `role="status"` +
     `aria-live="polite"`) so the click is never a silent black window.
   - **Stale-projection fallback** (Barry's block): if the hidden poll reaches a
     terminal status *while the runtime query still reports `update_available`*
     (refresh lagged / abnormal), the prompt is still open. It then shows the
     `ready_to_apply`/`completed` outcome (existing `update.status.*` copy) and
     **hides the action button** — never reverting to `Update now` (which would
     invite a pointless re-click on an already-staged update). `Not now` is the
     only dismiss; still no pinned modal and no new key.

4. **a11y** — wrap the live status line in `aria-live="polite"` (+ `role="status"`)
   so the state transition is announced; the black window is a feedback vacuum for
   sighted and screen-reader users alike. Keep the update button's disabled/label
   states in sync.

5. **i18n** — reuse the existing complete `update.status.*` set (all 4 locales
   already have it, incl. `ready_to_apply`). The dialog currently uses the sparse
   `update_prompt.status.completed`; switch it to `update.status.*` for the full
   set. **Net new keys: aim for zero**; if an announce-template is needed, add one
   key ×4 locales (en/ja/ko/zh-Hans).

6. **Tests** — `runtime-update-dialog.test.tsx`:
   - `ready_to_apply` from poll → poll stops, `refreshRuntimes` called, staged copy
     shown, button not stuck in active.
   - Intermediate `running` → labeled "Updating…", button disabled/spinner.
   - Contract-derived: runtime with `update_state: ready_to_apply` shows staged
     state without a poll result.
   - `aria-live` region present.
   Plus `core/runtimes/update-status.test.ts` for the pure derivation +
   terminal-set (table-driven over all `RuntimeUpdateState`/`RuntimeHealthState`).

## Eligibility + same-source presentation (Barry review round 2)

The BE collapses `ready_to_apply` into `runtime_health: "update_available"`, and
`runtimeCanStartSelfUpdate` only read health — two consequences, one root cause
(`update_state` was ignored at the canonical boundaries):

1. **Prompt re-pin**: once the durable `ready_to_apply` projection arrives, the
   staged runtime re-enters eligibility and the prompt re-opens, pinning a
   terminal modal. Fix: `runtimeCanStartSelfUpdate` excludes
   `isUpdateLifecycleActive(update_state)` = `{pending, running, ready_to_apply}`.
   `completed` and `idle` stay eligible (a newer release during the ~6h terminal
   `completed` window must be startable); `failed`/`timed_out` are handled by the
   existing retry surface, not the auto-prompt.
2. **Divergent badges**: `runtime-columns HealthCell`, `agent-profile-card`,
   `agent-side-panel` read raw `runtime_health`, so a staged runtime shows
   "Update available" instead of "Ready to apply". Fix: shared
   `deriveRuntimeHealthPresentation(runtime): RuntimeHealthState | "ready_to_apply"`
   — `ready_to_apply` overrides; `pending`/`running` → `updating`;
   `idle`/`completed`/`failed`/`offline` fall through to `runtime_health`. The
   badge visual/label layer (`RUNTIME_HEALTH_STATE_VISUAL`,
   `useRuntimeHealthStateLabel`, `RuntimeHealthStateBadge`) accepts the extended
   presentation; all three surfaces + `UpdateSection` now read from one source.
   One new i18n key `runtime_health.ready_to_apply` ×4 (concise badge label; the
   full "applies when idle" sentence stays in `update.status.ready_to_apply`).

Regressions: `isUpdateLifecycleActive` + `deriveRuntimeHealthPresentation` +
`runtimeCanStartSelfUpdate` (ready_to_apply ineligible, completed eligible) in
core; Runtime-list `HealthCell`, `AgentProfileCard`, `AgentSidePanel` each prove
the ready copy; Dialog proves no re-prompt for a staged runtime.

## Review round 3 (Barry)

1. **Machine header vs row contradiction**: `runtime-machines.ts` aggregated raw
   `aggregateRuntimeHealthState`, so a machine header showed "Update available"
   while its row's `HealthCell` showed "Ready to apply". Fix: shared
   `aggregateRuntimeHealthPresentation` (per-runtime `deriveRuntimeHealthPresentation`
   then highest presentation priority `ok < update_available < ready_to_apply <
   updating < offline < failed`); `runtime-machines` consumes it and
   `machine.runtimeHealth` is now `RuntimeHealthPresentation | null`.
2. **Offline precedence**: `deriveRuntimeHealthPresentation` checked the lifecycle
   before health, so `offline + ready_to_apply|running` mis-read as staged/updating.
   Fix: **fail closed to `offline` first**, then lifecycle override — a
   disconnected daemon can't be downloading or staged (mirrors the server's
   offline-first `deriveRuntimeHealth`).

Regressions: core `offline+ready`/`offline+running` → offline;
`aggregateRuntimeHealthPresentation` (staged surfaces ready_to_apply over a
sibling update_available; offline/failed dominate); real `buildRuntimeMachines`
machine header shows `ready_to_apply` for a staged runtime and `offline` when a
sibling is offline. `completed`/newer-release eligibility unchanged.

## #686 parallelism

Build strictly on the current public enums (unchanged). If #686 adds wire fields,
they land in **one adapter** (the `deriveUpdateStatus` input mapping) — never a
second state machine.

## Red lines

- No new notice/channel row, no session row (Parker + Barry).
- One contract, one derivation — no duplicated state machine.
- `ready_to_apply` is terminal everywhere.
- All three states visible; no unlabeled dead window.

## Gates

core+views typecheck 0 / eslint 0 / react:doctor Great 0 / dialog+derivation tests
green / 768-desktop served re-verify of the prompt flow.
