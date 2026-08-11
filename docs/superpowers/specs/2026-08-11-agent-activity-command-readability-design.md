# Agent Activity command readability

**Status:** Accepted (visual draft approved 2026-08-11).  
**Date:** 2026-08-11  
**Surface:** Agent detail **Activity tab** (`ActivityTab` + server-projected
`RunnerActivityTimelineRow`).  
**Visual draft:** [`artifacts/agent-activity-command-readability-draft.html`](../../../artifacts/agent-activity-command-readability-draft.html)  
**Out of scope for this change:** list Idle/Working band, composer strip, BE
event model, diagnostics, side panels.

> On current `dev`, Activity is Runner-projected (`title` / `subtext` / `body`).
> The legacy `activity-event` stream is gone. This design still applies:
> **default-full body** on body-bearing rows; timeline shell frozen.

## Problem

The Activity **timeline design is fine** (spine, chronological stream,
scroll/history). The failure mode is **body detail is hard to read without
extra clicks**:

1. Body-bearing rows always go through expand chrome even when the only payload
   is the sanitized body string.
2. Collapsed state uses CSS `line-clamp-2` on `subtext`, so typical multi-flag
   commands are truncated before the meaningful tail.
3. Copy only appears after expand, so the primary recovery action for a clipped
   body is easy to miss.

Source-backed data is already present on `RunnerActivityTimelineRow.body`
(server-projected, presentation-safe). This is a **presentation default** bug,
not a missing-field bug.

## Goals

1. **Default-readable commands** on the full Activity tab: a normal-length
   redacted command is fully visible without clicking.
2. **Always-available Copy** on command rows (not hover-gated).
3. **No timeline redesign**: keep spine, ordering, tones, Idle merge,
   jump-to-latest, older-page load, English-only chrome/labels, presentation
   keys, and narrative/diagnostic filtering.
4. **Pathological length still bounded**: unbounded DOM for multi-kb dumps is
   not required; long commands get an explicit soft fold, not a silent
   two-line clip.

## Non-goals

- Changing Activity information architecture (no run cards, no side detail
  panel, no regrouping by task).
- Changing BE timeline contract, `activity_kind` / `detail_kind` rules, or
  redaction policy.
- Re-opening unknown-tool safety (`performing_action`, no raw slug / no forged
  command from unmapped tools — #601).
- Changing profile **compact** Recent activity (still label + time only; no
  full command body in the popover).
- List-row Activity band, live Online/Offline, composer strip verbs.
- Path / Thinking / Output density (optional follow-up; not this draft’s must).

## Frozen shell (do not change)

These stay as they are today unless a bugfix is strictly required for the
command change:

| Area | Keep |
|------|------|
| Stream | Oldest → newest; land on latest; auto-follow only at bottom |
| History | Top sentinel + cursor `loadOlder` + scroll re-anchor |
| Spine | 1.5px continuous spine; static tone dots (`ACTIVITY_TONE_DOT_CLASS`) |
| Projection | `activityPresentation` / `isNarrativeActivityEvent` / Idle merge |
| Labels | English-only `ACTIVITY_LABEL_EN` / `ACTIVITY_CHROME_EN` |
| Security | Unmapped tool → generic row, no command expand |
| Compact | Profile peek: last N narrative labels, no expand, no command block |

## Target behavior

### Command rows (`subtextKind === "command"` with expandable command content)

**Collapsed / default (normal length):**

```text
● Running command…                          2m ago    [Copy]
  ┌─────────────────────────────────────────────────────────┐
  │ multica message send --channel … --body "…"             │
  │ (full redacted command, pre-wrap, no line-clamp-2)      │
  └─────────────────────────────────────────────────────────┘
```

Rules:

1. Render the **full redacted command** in the mono `CommandBlock` by default
   (`whitespace-pre-wrap`, `break-words`). **No `line-clamp-2`** for normal
   length.
2. **Copy is always visible** on the command surface (keyboard/focus still
   required for a11y; do not rely on hover).
3. **No chevron / expand control** when the command is within the soft length
   budget. Expanding empty chrome is noise.
4. Active tool still appends `…` on the **label** only (`Running command…`);
   the command body is never ellipsis-mangled for display when fully shown.

**Long command (soft fold):**

A command is “long” when either:

- line count after normalize ≥ **12**, or
- character length ≥ **2000** (after the existing DOM safety considerations)

Then:

1. Default shows the first **8** lines (or a character-safe prefix that does not
   split mid-line when practical) inside the same mono block.
2. A single control: **Show full command** / **Show less** (English-only chrome
   keys added next to existing `ACTIVITY_CHROME_EN`).
3. Full open state may use an internal scroll region with a **generous** cap
   (e.g. `max-h-[min(60vh,480px)]`) so multi-kb dumps do not blow the page;
   normal and mid-length commands never hit this scroller.
4. Copy always copies the **full** redacted command (`subtextFull`), never the
   folded prefix.

**Command without `entries[].command` (clip only):**

Keep today’s fallback: `tool_target` / presentation `subtext` as mono text.
Prefer wrap over aggressive single-line truncate where the layout already
allows; do not invent a full command from other fields.

### Non-command rows

Unchanged by this draft:

- Thinking / Output expand + markdown surface
- Path tools (`ToolTargetPath`)
- Status / Idle / failure / freshness-hold rows
- Decay styling for old failures

## Implementation sketch (FE only)

Primary files:

- `packages/views/agents/components/tabs/activity-tab.tsx` — body-bearing rows
  render a mono block (default-full + always-visible Copy); soft fold only when
  long; no-body rows keep title + unclamped subtext
- `packages/views/agents/components/tabs/activity-command-body.ts` — pure
  `isLongActivityCommand` / `foldActivityCommandPreview`
- Tests: `activity-tab.test.tsx`, `activity-command-body.test.ts`
- Locales: `show_full_command` / `show_less_command` (en + zh-Hans)

No API / sqlc / daemon changes.

## Success criteria

1. Fixture with a 3–6 line typical `multica …` command: **visible in full on
   first paint**, Copy works without expand.
2. Fixture with a 20+ line command: first paint shows fold UI + partial body;
   one click reveals the rest; Copy still full text.
3. Timeline shell tests (spine present when populated, Idle merge, jump-to-
   latest wiring) remain green without layout rewrites.
4. Compact profile Recent activity snapshot behavior unchanged.
5. Unknown / unmapped tool still never renders raw slug or forged command.

## Open points (resolve before implement if needed)

1. **Exact long thresholds** (12 lines / 2000 chars / 8-line preview) — product
   can tune; defaults above are starting numbers.
2. **Whether path / thinking preview follow-ups** ship in a second PR (default
   **yes, separate**).
3. **Active `…` on label** when command body is fully shown — keep current
   in-progress label signal (raft-aligned); do not put `…` on the command body.

## Spec self-check (draft)

- [x] No timeline IA redesign
- [x] Command default-full is explicit
- [x] Long-command fold is explicit (not silent 2-line clamp)
- [x] Compact / BE / security non-goals listed
- [x] User approved HTML draft (side-by-side before/after)

## Next step

1. Write an implementation plan under `docs/superpowers/plans/`.
2. Implement FE-only with tests; no BE work.
3. Long-command thresholds (12 lines / 2000 chars / 8-line preview) ship as defaults; tune only if QA complains.
`)