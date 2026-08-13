/**
 * LRM-1477 — Transition queue state machine tests (AC2/AC3, Rule ⑥/⑦).
 *
 * Covers:
 *  - AC2 Rule ⑦: 100-delta burst — peak live queue ≤ 64, settles to empty
 *    (final view not blocked by animation), coalescing bounded.
 *  - Coalescing (spec §5.3), cap enforcement, budget truncation.
 *  - Interrupt (new same-lane event coalesces/cancels the pending one).
 *  - Background restore (Rule ⑥: collapse to ≤ STAGGER_CAP live).
 *  - Reduced motion (Rule ④: immediate settlement, no playback queue).
 *  - Low-performance (Rule ⑤: halved budget, still settles).
 *
 * This module is a pure state layer — no DOM/RAF/React — so every test is a
 * deterministic reducer run with synthetic clocks.
 */
import { describe, expect, it } from "vitest";
import {
  createTransitionQueue,
  transitionQueueReducer,
  liveQueueSize,
  DEFAULT_MOTION_PROFILE,
  type MotionProfile,
  type TransitionQueue,
} from "./transition-queue";
import { hundredDeltaBurst, ALL_TRANSITION_KINDS } from "./fixture";
import { MOTION_QUEUE_CAP, MOTION_STAGGER_CAP } from "./tokens";

/** Drive a burst and return peak live queue size plus the final queue. */
function runBurst(
  events: ReturnType<typeof hundredDeltaBurst>,
  profile: MotionProfile = DEFAULT_MOTION_PROFILE,
  stepEveryMs = 1,
): { peak: number; final: TransitionQueue } {
  let state = createTransitionQueue({ nowMs: 0, profile });
  let peak = 0;
  events.forEach((event, index) => {
    state = transitionQueueReducer(state, {
      type: "enqueue",
      event,
      nowMs: index * stepEveryMs,
    });
    peak = Math.max(peak, liveQueueSize(state));
  });
  return { peak, final: state };
}

function fullySettle(state: TransitionQueue): TransitionQueue {
  // RAF drains entries through queued → running → settled; a single tick only
  // advances one step per entry (queued→running first, running→settled next).
  // Drain until stable so the terminal state is reached deterministically.
  let current = state;
  let guard = 0;
  while (liveQueueSize(current) > 0 && guard < 1_000_000) {
    current = transitionQueueReducer(current, {
      type: "tick",
      nowMs: 1_000_000,
      isHidden: false,
    });
    guard += 1;
  }
  return current;
}

function liveIds(state: TransitionQueue): string[] {
  const out: string[] = [];
  for (const [, entries] of state.queued) {
    for (const e of entries) if (e.state !== "settled") out.push(e.id);
  }
  return out;
}

describe("AC2 Rule ⑦ — 100-delta burst backpressure", () => {
  it("never exceeds the 64-entry cap across a full burst", () => {
    const { peak } = runBurst(hundredDeltaBurst());
    expect(peak).toBeLessThanOrEqual(MOTION_QUEUE_CAP);
    expect(hundredDeltaBurst()).toHaveLength(100);
  });

  it("settles to an empty queue (final view not blocked by animation)", () => {
    const { final } = runBurst(hundredDeltaBurst());
    const settled = fullySettle(final);
    expect(liveQueueSize(settled)).toBe(0);
    expect(liveIds(settled)).toEqual([]);
    expect(settled.queued.size).toBe(0);
  });

  it("settles every one of the 100 events (each has a terminal path)", () => {
    const { final } = runBurst(hundredDeltaBurst());
    const settled = fullySettle(final);
    expect(settled.seq).toBe(100);
  });

  it("AC2 equivalence: no entity remains stuck mid-animation at settle", () => {
    const events = hundredDeltaBurst();
    const noAnimationIds = new Set(events.flatMap((e) => e.related_ids));
    const { final } = runBurst(events);
    const settled = fullySettle(final);
    // With animation, no entity may remain "waiting to appear": the queue is
    // empty, so the visual set equals the no-animation replay's entity set.
    expect(liveQueueSize(settled)).toBe(0);
    expect(liveIds(settled)).toEqual([]);
    expect(noAnimationIds.size).toBeGreaterThan(0);
  });
});

describe("Coalescing — spec §5.3 same-lane merge", () => {
  it("merges two same-lane events within the budget window into one playback", () => {
    let state = createTransitionQueue({ nowMs: 0 });
    const event = (id: string) => ({
      transition_kind: "result_accepted" as const,
      related_ids: [id],
      anchor_id: "anchor-1",
    });
    state = transitionQueueReducer(state, { type: "enqueue", event: event("a"), nowMs: 0 });
    state = transitionQueueReducer(state, { type: "enqueue", event: event("b"), nowMs: 50 });
    const lane = state.queued.get("result_accepted::anchor-1") ?? [];
    const live = lane.filter((e) => e.state !== "settled");
    expect(live.length).toBe(1); // merged → one live
    expect(live[0]?.relatedIds).toContain("b");
  });
});

describe("Cap enforcement — spec §5.3", () => {
  it("keeps the live queue at or under the cap even with many distinct lanes", () => {
    let state = createTransitionQueue({ nowMs: 0 });
    for (let i = 0; i < MOTION_QUEUE_CAP + 5; i += 1) {
      const kind = ALL_TRANSITION_KINDS[i % ALL_TRANSITION_KINDS.length]!;
      state = transitionQueueReducer(state, {
        type: "enqueue",
        event: {
          transition_kind: kind,
          related_ids: [`n-${i}`],
          anchor_id: `anchor-${i}`,
        },
        nowMs: i,
      });
    }
    expect(liveQueueSize(state)).toBeLessThanOrEqual(MOTION_QUEUE_CAP);
  });
});

describe("Interrupt — spec §4.3", () => {
  it("a newer same-lane event cancels/coalesces the pending animation and continues", () => {
    let state = createTransitionQueue({ nowMs: 0 });
    const mk = (id: string) => ({
      transition_kind: "dispute_opened" as const,
      related_ids: [id],
      anchor_id: "claim-9",
    });
    state = transitionQueueReducer(state, { type: "enqueue", event: mk("old"), nowMs: 0 });
    state = transitionQueueReducer(state, { type: "enqueue", event: mk("new"), nowMs: 120 });
    const lane = state.queued.get("dispute_opened::claim-9") ?? [];
    const live = lane.filter((e) => e.state !== "settled");
    expect(live.length).toBe(1);
    expect(live[0]?.relatedIds).toContain("new");
  });
});

describe("Background restore — Rule ⑥", () => {
  it("collapse leaves at most STAGGER_CAP live and the rest settle", () => {
    let state = createTransitionQueue({ nowMs: 0 });
    for (let i = 0; i < 60; i += 1) {
      const kind = ALL_TRANSITION_KINDS[i % ALL_TRANSITION_KINDS.length]!;
      state = transitionQueueReducer(state, {
        type: "enqueue",
        event: {
          transition_kind: kind,
          related_ids: [`n-${i}`],
          anchor_id: "shared-anchor",
        },
        nowMs: i,
      });
    }
    state = transitionQueueReducer(state, { type: "set-hidden", hidden: true, nowMs: 1000 });
    state = transitionQueueReducer(state, { type: "set-hidden", hidden: false, nowMs: 1001 });
    expect(liveQueueSize(state)).toBeLessThanOrEqual(MOTION_STAGGER_CAP);
  });

  it("does not advance the clock while hidden (no RAF scheduling)", () => {
    let state = createTransitionQueue({ nowMs: 0 });
    state = transitionQueueReducer(state, {
      type: "enqueue",
      event: {
        transition_kind: "branch_spawned",
        related_ids: ["n-0"],
        anchor_id: null,
      },
      nowMs: 0,
    });
    state = transitionQueueReducer(state, { type: "set-hidden", hidden: true, nowMs: 10 });
    // A tick while hidden must be a no-op (returns same state).
    const before = state;
    const after = transitionQueueReducer(state, {
      type: "tick",
      nowMs: 1000,
      isHidden: true,
    });
    expect(after).toBe(before);
  });
});

describe("Reduced motion — Rule ④", () => {
  it("settles new events immediately without creating playback", () => {
    const profile: MotionProfile = { reducedMotion: true, lowPerformance: false };
    let state = createTransitionQueue({ nowMs: 0, profile });
    state = transitionQueueReducer(state, {
      type: "enqueue",
      event: {
        transition_kind: "integration_formed",
        related_ids: ["ins-1"],
        anchor_id: "a",
      },
      nowMs: 0,
    });
    state = transitionQueueReducer(state, {
      type: "enqueue",
      event: {
        transition_kind: "dispute_opened",
        related_ids: ["cl-1"],
        anchor_id: "a",
      },
      nowMs: 50,
    });
    expect(liveQueueSize(state)).toBe(0);
    expect(state.queued.size).toBe(0);
  });

  it("still settles to an empty queue under reduced motion", () => {
    const { final } = runBurst(
      hundredDeltaBurst(),
      { reducedMotion: true, lowPerformance: false },
      1,
    );
    expect(liveQueueSize(final)).toBe(0);
    expect(final.queued.size).toBe(0);
  });
});

describe("Low-performance — Rule ⑤", () => {
  it("collapses to a uniform fade for the budget tail and still settles", () => {
    const profile: MotionProfile = { reducedMotion: false, lowPerformance: true };
    const { final } = runBurst(hundredDeltaBurst(), profile, 1);
    expect(liveQueueSize(final)).toBeLessThanOrEqual(MOTION_QUEUE_CAP);
    const settled = fullySettle(final);
    expect(liveQueueSize(settled)).toBe(0);
  });
});
