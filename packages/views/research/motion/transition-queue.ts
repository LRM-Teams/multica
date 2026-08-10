/**
 * LRM-1477 — Transition queue (pure function state layer).
 *
 * Spec: §5 (transition queue, backpressure & coalescing), §5.3 (merge/cap/
 * budget), §5.4 (Rule ⑦ — final view identical to a no-animation replay),
 * §4.3 (interrupt), §4.4 (background restore).
 *
 * Design: side-effect-free and DOM/RAF/React-free so it can be unit-tested
 * deterministically. The React hook (useSemanticTransition) owns timers and
 * DOM, this module owns the state machine and policy rules only.
 *
 * Invariants
 *  - Every entry that is enqueued eventually reaches `settled` (either by
 *    playing to its planned end, by being coalesced into a newer sibling, or
 *    by the queue cap / budget truncation force-settling it). Animation never
 *    prevents the final view from matching a no-animation replay (AC2).
 *  - Coalescing: same laneKey (kind + anchor) events within the budget window
 *    merge into one playback and are marked `coalesced`, so a burst plays once
 *    (matches LRM-1400).
 *  - Cap: the queue never exceeds MOTION_QUEUE_CAP; oldest non-started entries
 *    are force-settled and dropped rather than played.
 *  - Background: while hidden, events are recorded but never scheduled for
 *    RAF; on restore the backlog collapses to at most MOTION_STAGGER_CAP live
 *    animations and the rest settle directly.
 */

import type { SemanticTransitionKind } from "./semantic-mapping";
import { resolveSemanticDisplay, effectiveVerb } from "./semantic-mapping";
import type {
  SemanticDisplaySpec,
  DisplayVerb,
  StaticMarker,
} from "./semantic-mapping";
import {
  MOTION_QUEUE_CAP,
  MOTION_STAGGER_CAP,
  MOTION_STAGGER_MS,
  MOTION_START_MS,
  MOTION_TOTAL_BUDGET_MS,
  MOTION_LOW_PERF_BUDGET_MS,
} from "./tokens";

// ─── Input contract (spec §5.1) ─────────────────────────────────────────────

/** Contract fixture shape for projection deltas (FE-01 未落地时用). */
export interface ProjectionTransitionEvent {
  transition_kind: SemanticTransitionKind;
  related_ids: string[];
  /** Appear/merge/escalate anchor (parent/target). */
  anchor_id?: string | null;
  /** Static status label (preserved under reduced motion). */
  status?: string | null;
}

// ─── Motion profile (spec §4) ────────────────────────────────────────────────

export interface MotionProfile {
  /** prefers-reduced-motion: all displacement → 0, uniform fade + instant layout. */
  reducedMotion: boolean;
  /** Low CPU/memory: 30fps throttle, no glow/blur, budget halved. */
  lowPerformance: boolean;
}

export const DEFAULT_MOTION_PROFILE: MotionProfile = {
  reducedMotion: false,
  lowPerformance: false,
};

// ─── Queue state (spec §5.2) ────────────────────────────────────────────────

export type QueuedEntryState = "queued" | "running" | "settled";

export interface QueuedEntry {
  id: string;
  event: ProjectionTransitionEvent;
  spec: SemanticDisplaySpec;
  /** Effective verb under the current profile. */
  verb: DisplayVerb;
  /** Static marker to retain after settling (Rule ②). */
  marker: StaticMarker;
  laneKey: string;
  relatedIds: string[];
  anchorId: string | null;
  status: string | null;
  /** Position in the batch schedule (drives stagger). */
  queueIndex: number;
  plannedStartMs: number;
  plannedEndMs: number;
  state: QueuedEntryState;
  /** True when this entry was merged into a newer sibling (never plays). */
  coalesced: boolean;
  /** True when this entry was dropped to settle due to cap/budget/background. */
  truncated: boolean;
  enqueuedMs: number;
}

export interface TransitionQueue {
  /** laneKey → not-yet-settled entries, oldest first. */
  queued: Map<string, QueuedEntry[]>;
  /** Monotonic id / index counter. */
  seq: number;
  /** document.hidden since timestamp (null when currently visible). */
  hiddenSinceMs: number | null;
  profile: MotionProfile;
}

export interface TransitionQueueOptions {
  nowMs: number;
  profile?: MotionProfile;
}

export function createTransitionQueue(
  opts: TransitionQueueOptions,
): TransitionQueue {
  return {
    queued: new Map(),
    seq: 0,
    hiddenSinceMs: null,
    profile: opts.profile ?? DEFAULT_MOTION_PROFILE,
  };
}

// ─── Lane key (spec §5.2) ────────────────────────────────────────────────────

/**
 * Coalescing lane: the semantic kind + its anchor. Two events of the same kind
 * anchored at the same target are candidates for merging within a budget
 * window (LRM-1400-compatible).
 */
export function laneKeyFor(
  kind: SemanticTransitionKind,
  anchorId: string | null | undefined,
): string {
  return `${kind}::${anchorId ?? ""}`;
}

// ─── Timings (spec §3) ───────────────────────────────────────────────────────

/** Per-verb duration table; reduced-motion collapses to a uniform fade. */
const VERB_DURATION_MS: Record<DisplayVerb, number> = {
  appear: 300,
  merge: 320,
  conflict: 320,
  escalate: 320,
  stale: 300,
  revise: 300,
  reappear: 260,
  camera: 400,
  retire: 300, // ⑤ 废弃 (LRM-1537 §3.1, stale family)
  restart: 240, // ⑥ 重启 (edge-draw family)
  regoal: 320, // ⑦ 目标修改 (≤ merge budget)
};

export function verbDurationMs(
  verb: DisplayVerb,
  profile: MotionProfile,
): number {
  if (profile.reducedMotion) return 200; // uniform fade-in
  return VERB_DURATION_MS[verb];
}

/** Schedule start for the Nth live slot of a batch, capped by stagger cap. */
export function batchStartMs(
  indexInBatch: number,
  profile: MotionProfile,
): number {
  if (profile.reducedMotion) return 0;
  const capped = Math.min(indexInBatch, MOTION_STAGGER_CAP);
  return MOTION_START_MS + capped * MOTION_STAGGER_MS;
}

function budgetMs(profile: MotionProfile): number {
  if (profile.lowPerformance) return MOTION_LOW_PERF_BUDGET_MS;
  return MOTION_TOTAL_BUDGET_MS;
}

// ─── Queue reducer ───────────────────────────────────────────────────────────

export type TransitionQueueAction =
  | { type: "enqueue"; event: ProjectionTransitionEvent; nowMs: number }
  | { type: "tick"; nowMs: number; isHidden: boolean }
  | { type: "settle-all"; nowMs: number }
  | { type: "set-hidden"; hidden: boolean; nowMs: number }
  | { type: "set-profile"; profile: MotionProfile; nowMs: number };

/** Prune settled entries, returning a fresh map with only live entries. */
function pruneSettled(
  queued: Map<string, QueuedEntry[]>,
): Map<string, QueuedEntry[]> {
  const next = new Map<string, QueuedEntry[]>();
  for (const [laneKey, entries] of queued) {
    const live = entries.filter((entry) => entry.state !== "settled");
    if (live.length > 0) next.set(laneKey, live);
  }
  return next;
}

/** Force every live entry to settled (terminal guarantee). */
function settleAll(queued: Map<string, QueuedEntry[]>, nowMs: number): void {
  for (const [, entries] of queued) {
    for (const entry of entries) {
      if (entry.state === "settled") continue;
      entry.state = "settled";
      entry.plannedEndMs = nowMs;
      entry.truncated = true;
    }
  }
}

export function transitionQueueReducer(
  state: TransitionQueue,
  action: TransitionQueueAction,
): TransitionQueue {
  switch (action.type) {
    case "settle-all":
      settleAll(state.queued, action.nowMs);
      return { ...state, queued: pruneSettled(state.queued) };

    case "set-hidden":
      if (action.hidden && state.hiddenSinceMs === null) {
        return { ...state, hiddenSinceMs: action.nowMs };
      }
      if (!action.hidden && state.hiddenSinceMs !== null) {
        const restored = restoreBacklog(state, action.nowMs);
        return { ...restored, hiddenSinceMs: null };
      }
      return state;

    case "set-profile": {
      settleAll(state.queued, action.nowMs);
      return {
        ...state,
        profile: action.profile,
        queued: pruneSettled(state.queued),
      };
    }

    case "tick": {
      if (action.isHidden) return state; // never advance clocks while hidden
      let changed = false;
      for (const [, entries] of state.queued) {
        for (const entry of entries) {
          if (entry.state === "queued" && action.nowMs >= entry.plannedStartMs) {
            entry.state = "running";
            changed = true;
          } else if (
            entry.state === "running" &&
            action.nowMs >= entry.plannedEndMs
          ) {
            entry.state = "settled";
            changed = true;
          }
        }
      }
      if (!changed) return state;
      return { ...state, queued: pruneSettled(state.queued) };
    }

    case "enqueue":
      return enqueueInto(state, action.event, action.nowMs);
  }
}

function enqueueInto(
  state: TransitionQueue,
  event: ProjectionTransitionEvent,
  nowMs: number,
): TransitionQueue {
  const spec = resolveSemanticDisplay(event.transition_kind);
  const laneKey = laneKeyFor(event.transition_kind, event.anchor_id);
  const verb = effectiveVerb(spec, state.profile);
  const duration = verbDurationMs(verb, state.profile);

  const liveCount = [...state.queued.values()].reduce(
    (sum, arr) => sum + arr.filter((e) => e.state !== "settled").length,
    0,
  );

  state.seq += 1;
  const id = `txn-${state.seq}`;

  // Coalesce (spec §5.3): same lane, prior not-yet-settled sibling within the
  // budget window merges into this new playback — plays once.
  const lane = state.queued.get(laneKey);
  const prior = lane?.find((e) => e.state !== "settled");
  if (prior && nowMs - prior.enqueuedMs <= budgetMs(state.profile)) {
    prior.coalesced = true;
    prior.state = "settled";
    prior.plannedEndMs = nowMs;
  }

  // Cap enforcement (spec §5.3): at/over cap, the oldest non-started entry
  // falls straight to settled (never plays).
  if (liveCount >= MOTION_QUEUE_CAP) {
    const oldestLive = oldestLiveEntry(state.queued);
    if (oldestLive) {
      oldestLive.state = "settled";
      oldestLive.plannedEndMs = nowMs;
      oldestLive.truncated = true;
    }
  }

  const indexInBatch = liveCount + 1;
  const batchStart = batchStartMs(indexInBatch, state.profile);
  const budget = budgetMs(state.profile);
  // Budget truncation (spec §5.3): if the tail would exceed the batch budget,
  // keep only a uniform fade (200ms) instead of a full displacement animation.
  const truncateTail = batchStart + duration > budget;
  const effectiveStart = truncateTail ? nowMs : batchStart + nowMs;
  const effectiveEnd = effectiveStart + (truncateTail ? 200 : duration);

  const entry: QueuedEntry = {
    id,
    event,
    spec,
    verb,
    marker: spec.marker,
    laneKey,
    relatedIds: event.related_ids ?? [],
    anchorId: event.anchor_id ?? null,
    status: event.status ?? null,
    queueIndex: indexInBatch,
    plannedStartMs: effectiveStart,
    plannedEndMs: effectiveEnd,
    state: "queued",
    coalesced: false,
    truncated: truncateTail,
    enqueuedMs: nowMs,
  };

  const nextLane = [...(state.queued.get(laneKey) ?? []), entry];
  const nextQueued = new Map(state.queued);
  nextQueued.set(laneKey, nextLane);
  return { ...state, queued: nextQueued };
}

function oldestLiveEntry(
  queued: Map<string, QueuedEntry[]>,
): QueuedEntry | null {
  let oldest: QueuedEntry | null = null;
  for (const [, entries] of queued) {
    for (const entry of entries) {
      if (entry.state === "settled") continue;
      if (!oldest || entry.enqueuedMs < oldest.enqueuedMs) oldest = entry;
    }
  }
  return oldest;
}

/**
 * Background restore (Rule ⑥): collapse the hidden-period backlog — coalesce
 * per lane, keep the first MOTION_STAGGER_CAP live, settle the rest to
 * terminal instantly. Never replays historical paths.
 */
function restoreBacklog(
  state: TransitionQueue,
  nowMs: number,
): TransitionQueue {
  const flat: QueuedEntry[] = [];
  for (const [, entries] of state.queued) {
    for (const e of entries) {
      if (e.state !== "settled") flat.push(e);
    }
  }
  flat.sort((a, b) => a.enqueuedMs - b.enqueuedMs);

  // Coalesce same-lane siblings that share the same anchor/kind.
  const seenLanes = new Map<string, QueuedEntry>();
  for (const entry of flat) {
    const prior = seenLanes.get(entry.laneKey);
    if (prior) {
      prior.coalesced = true;
      prior.state = "settled";
      prior.plannedEndMs = nowMs;
    }
    seenLanes.set(entry.laneKey, entry);
  }

  const survivors = [...seenLanes.values()].sort(
    (a, b) => a.enqueuedMs - b.enqueuedMs,
  );
  const capped = survivors.slice(0, MOTION_STAGGER_CAP);
  const toSettle = survivors.slice(MOTION_STAGGER_CAP);

  for (const entry of toSettle) {
    entry.state = "settled";
    entry.plannedEndMs = nowMs;
    entry.truncated = true;
  }

  // Re-schedule the surviving cap as a fresh, immediate batch.
  capped.forEach((entry, index) => {
    entry.state = "queued";
    entry.plannedStartMs = batchStartMs(index, state.profile) + nowMs;
    entry.plannedEndMs =
      entry.plannedStartMs + verbDurationMs(entry.verb, state.profile);
  });

  const nextQueued = new Map<string, QueuedEntry[]>();
  for (const entry of [...capped, ...toSettle]) {
    if (entry.state === "settled") continue;
    const lane = nextQueued.get(entry.laneKey) ?? [];
    lane.push(entry);
    nextQueued.set(entry.laneKey, lane);
  }

  return { ...state, queued: nextQueued };
}

// ─── Read helpers for consumers/hook ─────────────────────────────────────────

/** Total number of live (non-settled) entries. */
export function liveQueueSize(state: TransitionQueue): number {
  let count = 0;
  for (const [, entries] of state.queued) {
    for (const e of entries) if (e.state !== "settled") count += 1;
  }
  return count;
}

/**
 * Per-entity static markers for currently-live entries that carry a marker
 * (Rule ②). The queue prunes settled entries, so this is the "in-flight"
 * marker set the consumer applies while animating; the persistent post-settle
 * marker is delivered via the directive's markerClass and the consumer's own
 * canonical status rendering.
 */
export function settledMarkers(
  state: TransitionQueue,
): Map<string, { marker: StaticMarker; status: string | null; verb: DisplayVerb }> {
  const markers = new Map<
    string,
    { marker: StaticMarker; status: string | null; verb: DisplayVerb }
  >();
  for (const [, entries] of state.queued) {
    for (const entry of entries) {
      if (entry.state === "settled" || entry.marker === "none") continue;
      for (const id of entry.relatedIds) {
        markers.set(id, {
          marker: entry.marker,
          status: entry.status,
          verb: entry.verb,
        });
      }
    }
  }
  return markers;
}
