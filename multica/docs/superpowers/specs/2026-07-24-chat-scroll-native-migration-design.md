# Chat scroll: retire the custom anchor layer in favor of Virtuoso-native behavior

Status: draft, awaiting team sign-off (Frank/Parker/Iris/Wren) before implementation.
Owner: Felix. Related: #348, #365, #883, #689 (PR #1146), Iris's `notes/chat-scroll-required-behaviors.md`.

## Problem

`channel-message-list.tsx` and its two hooks (`use-unread-anchor-scroll.ts`,
`use-new-arrivals-pill.ts`) have accumulated several incident-scarred custom
scroll mechanisms on top of react-virtuoso (`customScrollParent`,
`firstItemIndex`, `followOutput`, `initialTopMostItemIndex`). Each was added to
fix a specific bug (#348 cold-load position, #365 an infinite-loop regression,
#883 measurement races, #689 mid-scroll jank) and each carries its own
incident-history comment. A fourth bug just surfaced (2026-07-24, Frank):
scrolled up reading history, a new message arrives, the viewport auto-scrolls
up/jumps instead of staying put. The team's read (Parker, Iris, Frank): stop
patching case-by-case and instead audit which custom layers are genuinely
still needed vs. which just fight behavior the library already provides
natively, then delete the latter wholesale.

## What's actually broken right now (new bug, unconfirmed root cause)

Symptom: scrolled away from bottom (reading history) + a live message arrives
→ viewport moves, should stay put.

Code path: `followOutput={() => (!loadingOlder && !isAnchorSettling &&
isNearBottom ? "smooth" : false)}` (channel-message-list.tsx:599).
`isNearBottom` is set from Virtuoso's own `atBottomStateChange` callback
(`atBottomThreshold={120}`). I read this path end-to-end and found no logic
bug in our code — `followOutput` is correctly gated on `isNearBottom`, and I
independently confirmed `useNewMessagesDivider`'s entry-high-water snapshot
(computeNewMessagesDivider) explicitly excludes live-arriving messages from
ever reopening `unreadAnchorIndex`/re-triggering the unread-anchor settle
effect — so it's not that path re-firing either.

Leading hypothesis (unverified, needs Iris's scrollTop-sampling or a real
device to confirm): `atBottomThreshold={120}` is generous enough that a user
who scrolls up only slightly still reads as "near bottom" to Virtuoso, so
`followOutput` correctly-by-its-own-logic still fires — the bug is a
threshold/expectation mismatch, not a broken gate. Needs verification before
any fix; do not lower the threshold blind.

## Audit: what's native vs. what's genuinely custom

Iris's full "must-preserve behaviors" checklist is the acceptance bar
(`notes/chat-scroll-required-behaviors.md`, summarized here):

| # | Behavior | Verdict | Notes |
|---|----------|---------|-------|
| 1 | Stick to bottom on new message when near bottom | Native (`followOutput`+`atBottomStateChange`) | Trust it once #2 is root-caused — same mechanism |
| 2 | Preserve position reading history when a message arrives | Native, currently broken | The active bug — likely `atBottomThreshold` tuning, not a missing feature |
| 3 | Preserve position on load-older (prepend) | Native (`firstItemIndex`) | Iris's emulation already shows reversals=0/backward=0 |
| 4 | Cold-load lands at bottom / correct position, no jank | Native (`initialTopMostItemIndex`) — **with a real caveat, see below** | |
| 5 | User gesture takes priority over auto-scroll | Semi-custom (#1146) | Already shipped: gesture-yield in the settle loop + kept |
| 6 | Return-to-position + highlight source message from issue/thread deep link | Fully custom (#592/#588) | Must stay — no library equivalent, this is app-specific navigation |

**Caveat on #4 / the #883 settle loop — UPDATED after checking upstream directly**:
[petyosi/react-virtuoso#883](https://github.com/petyosi/react-virtuoso/issues/883)
("Setting initial scrolling position is racy") was **closed in April 2023**
by a real merged fix ("initialScrollTop not working w/o initialItemCount"),
years before our comments were written and long before any version we've run
(4.14–4.18). So our settle loop isn't guarding a *currently open* upstream
bug by that exact issue number — either we hit a distinct-but-similar race
that got mis-attributed to #883 when the comment was written, or the
workaround has been defensive/redundant for a while. **I cannot determine
which from this environment**: verifying "does bare `initialTopMostItemIndex`
converge reliably on a real large unmeasured list" requires a real browser —
jsdom has no real layout engine, so any test here would only be checking our
own mocked geometry, not genuine timing behavior. This is exactly the
`no-browser-automation-gap` limitation.

Given the history here (#365: a well-intentioned scroll change shipped without
this kind of verification took down all chats), I am NOT removing the settle
loop in this pass on documentation-only grounds. Options, in order of
preference:
1. **Ship this PR without touching the settle loop**, corrected comments only
   (its cited justification was inaccurate, but its behavior is unchanged and
   still correct) — defer actual removal to a follow-up once real-device/
   browser tooling exists to verify convergence empirically.
2. If the team wants to attempt removal in this same PR despite the
   verification gap: gate it as a fully reversible, isolated change (e.g.
   behind an easy revert point) and require Frank's real-device pass on a
   genuinely large/far-scroll conversation specifically (not just the happy
   path) before merge — never ship "probably fine" on this file without that
   check, per the incident history.

## Plan (sequenced, each step independently verifiable)

1. **Root-cause the new bug (#2) first** — it's live and reported, independent
   of the rest of this plan. Likely fix: instrument `atBottomStateChange`/
   `isNearBottom` transitions (Iris's scrollTop-sampling method) to confirm
   whether it's a threshold issue or something else. Small, targeted fix,
   ships on its own.
2. **Upgrade react-virtuoso** to latest 4.x, run full existing scroll test
   suite (`channel-message-list.test.tsx`, `use-unread-anchor-scroll.test.ts`,
   any others) against the new version with zero other changes — isolates
   "did the upgrade alone change behavior" from any of our edits.
3. **Re-evaluate the #883 settle loop** per the caveat above, with the 14k-px
   far-jump case as the acceptance test either way.
4. **Delete confirmed-redundant custom code** for items 1/3/4 once each is
   verified native-covered post-upgrade (Iris's numeric pass + Frank's
   real-device pass per item, matching her checklist's per-item verification
   note).
5. **Leave 5/6 alone** — they're genuine product requirements with no library
   equivalent.

Each step ships as its own small, reviewable, real-device-verified change —
not one big rewrite PR. #1146 already is step 4 partially done (removed the
redundant per-row ResizeObserver, added gesture-yield). This doc supersedes
ad-hoc "let's also fix X while we're in this file" patching for this
subsystem going forward.

## Non-goals

- Not evaluating a commercial/managed Virtuoso chat component in this pass
  (Parker's "plan B") — only after plan A is exhausted and still unstable.
- Not touching #592/#588 deep-link/highlight logic (item 6) — out of scope,
  no library equivalent exists.
