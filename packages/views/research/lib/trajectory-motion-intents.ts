/**
 * LRM-1400 — trajectory motion intent / budget pure state layer (parent LRM-1393).
 *
 * Scope: normalize the four trajectory events (branch-grow / commit-append /
 * merge-flow / checkout-focus) into independent, cancellable animation intents;
 * merge same-lane consecutive events inside a budget window; drop anything
 * enqueued while the document is hidden; degrade under reduced-motion /
 * low-performance. This file is deliberately UI-free: it does not touch layout
 * segments, does not schedule RAF, and does not import React components.
 *
 * Layering: the trajectory DAG data model (LRM-1389, packages/core) is data;
 * this file is the *animation-intent* state on top of it. Lane ids refer to
 * the same branch lanes produced by `git-topology.ts`.
 */

/** The four trajectory motion events (LRM-1393 AC 1). */
export type TrajectoryMotionKind =
  | "branch-grow"
  | "commit-append"
  | "merge-flow"
  | "checkout-focus";

/** Degradation profile resolved once per frame-loop/tick, not per event. */
export interface TrajectoryMotionProfile {
  /** prefers-reduced-motion: reduce → zero path displacement, keep static highlight. */
  reducedMotion: boolean;
  /** low-performance device/profile → caller may drop to 30fps and disable glow. */
  lowPerformance: boolean;
}

/** A normalized motion event arriving from a WS update or local state change. */
export interface TrajectoryMotionEvent {
  kind: TrajectoryMotionKind;
  /** Lane the event belongs to; merge/grow coalescing is scoped by lane. */
  lane: string;
  /** Target element ids (commit / branch / node ids) for this event. */
  targetIds: string[];
  /** Static status label to keep even under reduced-motion (e.g. after a node). */
  status?: string | null;
}

/**
 * A committed, cancellable animation intent. Two intents for the same
 * lane+kind inside a budget window coalesce; cancelling an older intent by id
 * lets a later event interrupt it and continue from current state.
 */
export interface TrajectoryMotionIntent {
  /** Unique generation token; cancel/absorb keyed by this id. */
  id: string;
  kind: TrajectoryMotionKind;
  lane: string;
  targetIds: string[];
  /** Monotonic arrival order; newest wins on coalesce. */
  seq: number;
  /** Arrival wall-clock for budget-window coalescing. */
  createdAtMs: number;
  /**
   * Normalized path displacement in px. Under reduced-motion this is forced
   * to 0 while `status` is still kept (static highlight/status intent).
   */
  displacementPx: number;
  /** Static highlight/status intent that survives reduced-motion. */
  status: string | null;
  /** Low-performance degradation flag (caller drops to 30fps / disables glow). */
  lowPerformance: boolean;
  /** True once cancelled by a newer competing event. */
  cancelled: boolean;
}

/** The single motion state container fed by `applyTrajectoryEvent`. */
export interface TrajectoryMotionState {
  intents: TrajectoryMotionIntent[];
  seq: number;
  /** Ms since epoch when document become hidden; null when visible. */
  hiddenSinceMs: number | null;
}

export const DEFAULT_TRAJECTORY_MOTION_BUDGET_MS = 320;
export const TRAJECTORY_MOTION_MAX_DISPLACEMENT_PX = 24;

let intentCounter = 0;
function nextIntentId(state: TrajectoryMotionState): string {
  intentCounter += 1;
  return `${state.seq}-${intentCounter}-${Date.now().toString(36)}`;
}

export function createTrajectoryMotionState(): TrajectoryMotionState {
  return {
    intents: [],
    seq: 0,
    hiddenSinceMs: null,
  };
}

/** Normalized per-event displacement; merge-flow fans out so it gets a modest push. */
function eventDisplacementPx(kind: TrajectoryMotionKind): number {
  switch (kind) {
    case "branch-grow":
      return 16;
    case "commit-append":
      return 8;
    case "merge-flow":
      return 20;
    case "checkout-focus":
      return 0; // focus intent is a highlight, not a path move
  }
}

/** True when both the same lane and same kind — the only coalescable pair. */
function coalescable(a: TrajectoryMotionIntent, kind: TrajectoryMotionKind, lane: string): boolean {
  return a.kind === kind && a.lane === lane;
}

/**
 * Reduce one motion event into `state`, mutating and returning it.
 *
 * Rules (LRM-1393 / LRM-1400 AC):
 *  1. Four kinds produce independent, cancellable intents; each event gets a
 *     fresh `id` so it can be cancelled/interrupted once a newer event lands.
 *  2. Same-lane consecutive branch-grow / commit-append coalesce inside
 *     `budgetMs`; merge-flow never merges across lanes (AC 2).
 *  3. checkout-focus is a singleton — a newer focus intent replaces the older
 *     one outright (AC 1).
 *  4. While the document is hidden (`hiddenSinceMs` set), events are NOT
 *     queued and are dropped; on recover only new events are processed (AC 3).
 *  5. reduced-motion → zero path displacement but status intent kept (AC 4);
 *     low-performance → `lowPerformance` flag surfaced on the intent (AC 4).
 */
export function applyTrajectoryEvent(
  state: TrajectoryMotionState,
  event: TrajectoryMotionEvent,
  profile: TrajectoryMotionProfile,
  nowMs: number,
  budgetMs: number = DEFAULT_TRAJECTORY_MOTION_BUDGET_MS,
): TrajectoryMotionState {
  // AC 3: while hidden we drop the event and never queue history for replay.
  if (state.hiddenSinceMs !== null) {
    return state;
  }

  state.seq += 1;

  // AC 1: checkout-focus replaces any prior focus intent (focus is exclusive).
  if (event.kind === "checkout-focus") {
    const intent: TrajectoryMotionIntent = {
      id: nextIntentId(state),
      kind: event.kind,
      lane: event.lane,
      targetIds: event.targetIds,
      seq: state.seq,
      createdAtMs: nowMs,
      displacementPx: 0,
      status: event.status ?? null,
      lowPerformance: profile.lowPerformance,
      cancelled: false,
    };
    state.intents = state.intents.filter((i) => i.kind !== "checkout-focus");
    state.intents.push(intent);
    return state;
  }

  const displacement = profile.reducedMotion ? 0 : eventDisplacementPx(event.kind);

  // AC 2: look for an active same-lane + same-kind intent inside the budget
  // window to coalesce into; merge-flow must stay within its own lane/kind.
  const coalesceIndex = state.intents.findIndex(
    (i) => !i.cancelled && coalescable(i, event.kind, event.lane) && nowMs - i.createdAtMs <= budgetMs,
  );

  if (coalesceIndex >= 0) {
    const prev = state.intents[coalesceIndex]!;
    state.intents[coalesceIndex] = {
      ...prev,
      seq: state.seq,
      createdAtMs: nowMs,
      // Absorb the new target ids; newest status wins; keep the newest
      // displacement (a grow that follows an append should reflect the latest).
      targetIds: Array.from(new Set([...prev.targetIds, ...event.targetIds])),
      status: event.status ?? prev.status,
      lowPerformance: prev.lowPerformance || profile.lowPerformance,
    };
    return state;
  }

  state.intents.push({
    id: nextIntentId(state),
    kind: event.kind,
    lane: event.lane,
    targetIds: event.targetIds,
    seq: state.seq,
    createdAtMs: nowMs,
    displacementPx: displacement,
    status: event.status ?? null,
    lowPerformance: profile.lowPerformance,
    cancelled: false,
  });
  return state;
}

/** Mark an intent by id cancelled, so a newer event can interrupt it. */
export function cancelTrajectoryIntent(state: TrajectoryMotionState, id: string): TrajectoryMotionState {
  state.intents = state.intents.map((i) => (i.id === id ? { ...i, cancelled: true } : i));
  return state;
}

/** Drop all cancelled intents (e.g. flush at the end of a frame/loop). */
export function flushCancelledTrajectoryIntents(state: TrajectoryMotionState): TrajectoryMotionState {
  state.intents = state.intents.filter((i) => !i.cancelled);
  return state;
}

/** Turn active intents into a stable selection suitable for CSS-class application. */
export function activeTrajectoryIntents(state: TrajectoryMotionState): TrajectoryMotionIntent[] {
  return state.intents.filter((i) => !i.cancelled);
}

/**
 * AC 3: visibility control. While hidden, no events are queued; on recover we
 * do NOT replay dropped history — callers simply skip missed frames and keep
 * only new events going forward.
 */
export function setTrajectoryMotionVisibility(
  state: TrajectoryMotionState,
  visible: boolean,
  nowMs: number,
): TrajectoryMotionState {
  state.hiddenSinceMs = visible ? null : nowMs;
  return state;
}

/** Resolve a degradation profile from native signals (pure, testable). */
export function trajectoryMotionProfile(input: {
  prefersReducedMotion: boolean;
  lowPerformance: boolean;
}): TrajectoryMotionProfile {
  return {
    reducedMotion: input.prefersReducedMotion,
    lowPerformance: input.lowPerformance,
  };
}
