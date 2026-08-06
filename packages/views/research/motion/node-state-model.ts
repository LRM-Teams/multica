/**
 * LRM-1407 — Research node lifecycle state transition model.
 *
 * Builds on the LRM-1477 semantic motion engine (packages/views/research/motion/):
 * this module is the <b>node-level state model</b> that maps a research node's
 * lifecycle state changes (问题产生 → 派工 → 完成 / 失败 → 重试 → 再派) onto the
 * engine's `ProjectionTransitionEvent` contract. It is the "usage / state model"
 * layer on top of the engine's queue + mapping — it never re-implements the
 * queue, coercion, budget or reduced-motion policies (those stay in the engine).
 *
 * Hard rules carried from the engine (spec §2.2 / Rule ⑦):
 *  - The model only ever emits display events; it never mutates canonical data,
 *    layout, or saved coordinates. All display verbs move transform/opacity only.
 *  - `failed` stops at a stable actionable marker (conflict) and does NOT
 *    re-bounce: a repeated `failed→failed` delta is idempotent and emits nothing.
 *    A real `failed→dispatched` (retry) reverses into a fresh re-enter event.
 *  - Consecutive WS updates are interruptible: each real transition emits one
 *    event; bursts collapse via the engine's lane coalescing / queue backpressure.
 *
 * Pure TS, side-effect-free, DOM/RAF/React-free (matches the engine modules) so
 * it is deterministically unit-testable. The React hook owns DOM/timers by
 * feeding returned events into `useSemanticTransition`.
 */

import type {
  ProjectionTransitionEvent,
  MotionProfile,
} from "./transition-queue";
import { DEFAULT_MOTION_PROFILE } from "./transition-queue";
import type { SemanticTransitionKind } from "./semantic-mapping";

// ─── Node lifecycle state (spec LRM-1407 AC1/AC2) ───────────────────────────

/**
 * The research node lifecycle phases that the UI must express as animation.
 * - `generated`  : a new task is spawned from its parent problem (appear).
 * - `dispatched` : a one-shot task flows to an Agent (appear + exec-badge).
 * - `succeeded`  : the result is accepted and flows back to the problem
 *                  (advance + accepted-check).
 * - `failed`     : stops at an actionable state (conflict marker), re-triable,
 *                  never infinite-bounces on its own.
 */
export type ResearchNodeLifecycle =
  | "generated"
  | "dispatched"
  | "succeeded"
  | "failed";

/** A node delta from the WS stream (or fixture) that we wish to express. */
export interface ResearchNodeStateInput {
  /** Stable node id (task / agent / result). */
  id: string;
  lifecycle: ResearchNodeLifecycle;
  /** Parent problem anchor — used for the "spawn / return" flows. */
  parentId?: string | null;
}

/** Per-node tracked state inside the model. */
export interface TrackedResearchNode {
  id: string;
  lifecycle: ResearchNodeLifecycle;
  parentId: string | null;
}

/** Snapshot carried by the model (pure state). */
export interface ResearchNodeStateModel {
  /** id → tracked node (only nodes that have appeared). */
  nodes: Map<string, TrackedResearchNode>;
  profile: MotionProfile;
}

export function createResearchNodeStateModel(
  profile: MotionProfile = DEFAULT_MOTION_PROFILE,
): ResearchNodeStateModel {
  return { nodes: new Map(), profile };
}

// ─── Transition → transition_kind (LRM-1407 AC1: spawn / flow / return) ─────
//
// Only <b>real</b> lifecycle transitions map to a display event. Idempotent
// duplicates (same node, same lifecycle) map to `null` so a repeating failed
// delta does not re-bounce the animation.

type NodeTransition =
  | "appear" // generated: new node from parent problem
  | "dispatch" // generated|failed → dispatched: flow to agent
  | "succeed" // dispatched → succeeded: return to problem
  | "fail" // dispatched → failed: pull-apart + actionable conflict
  | "retry"; // failed → dispatched: re-enter after retry

/**
 * Classify a real lifecycle change into a transition + the transition_kind,
 * plus the anchor the event should be attached to.
 * `parentId` drives the "spawn from problem" and "return to problem" flows.
 */
export function transitionForLifecycleChange(
  prev: ResearchNodeLifecycle,
  next: ResearchNodeLifecycle,
  parentId: string | null,
): { transition: NodeTransition; kind: SemanticTransitionKind; anchorId: string | null } | null {
  if (prev === next) return null; // idempotent — never re-bounce (AC2)

  switch (next) {
    case "generated":
      // fresh node appears, anchored to its parent problem
      return {
        transition: "appear",
        kind: "branch_spawned",
        anchorId: parentId,
      };
    case "dispatched": {
      const retry = prev === "failed";
      return {
        transition: retry ? "retry" : "dispatch",
        kind: "task_dispatched",
        anchorId: retry ? null : parentId,
      };
    }
    case "succeeded":
      return {
        transition: "succeed",
        kind: "result_accepted",
        anchorId: parentId, // result flows back to the problem
      };
    case "failed":
      return {
        transition: "fail",
        kind: "dispute_opened", // actionable conflict marker (AC2)
        anchorId: parentId,
      };
  }
}

// ─── First-appearance mapping ──────────────────────────────────────────────
//
// A node that first appears in a given lifecycle still expresses its entry
// as a display event (the “spawn / flow” the AC calls for). This bypasses the
// prev===next idempotency guard used for repeated deltas.

export function appearanceEventFor(
  lifecycle: ResearchNodeLifecycle,
  parentId: string | null,
): {
  kind: SemanticTransitionKind;
  anchorId: string | null;
} | null {
  switch (lifecycle) {
    case "generated":
      return { kind: "branch_spawned", anchorId: parentId };
    case "dispatched":
      return { kind: "task_dispatched", anchorId: parentId };
    case "succeeded":
      return { kind: "result_accepted", anchorId: parentId };
    case "failed":
      return { kind: "dispute_opened", anchorId: parentId };
  }
}

// ─── Reducer: apply a batch of node deltas ──────────────────────────────────

export interface ResearchNodeStateAction {
  type: "apply";
  /** Ordered list of node deltas from the current WS batch. */
  deltas: ResearchNodeStateInput[];
}

export interface ApplyNodeDeltasResult {
  state: ResearchNodeStateModel;
  /** Display events to feed into the LRM-1477 transition queue. */
  events: ProjectionTransitionEvent[];
}

/**
 * Apply a batch of node lifecycle deltas.
 *
 * - Tracks each node's last lifecycle so repeated identical deltas are
 *   idempotent (no infinite bounce, AC2).
 * - Emits one `ProjectionTransitionEvent` per real transition, tagged with the
 *   node's id as `related_ids` and the flow anchor as `anchor_id`.
 * - Consecutive bursts remain interruptible because the returned events go
 *   through the engine's lane coalescing / queue backpressure (AC3/AC5).
 */
export function applyNodeDeltas(
  model: ResearchNodeStateModel,
  action: ResearchNodeStateAction,
): ApplyNodeDeltasResult {
  const nodes = new Map(model.nodes);
  const events: ProjectionTransitionEvent[] = [];

  for (const delta of action.deltas) {
    const parentId = delta.parentId ?? null;
    const prior = nodes.get(delta.id);
    const prev = prior?.lifecycle ?? null;

    if (prev === delta.lifecycle) {
      // Idempotent — no real change. Keep tracking; emit nothing (AC2).
      nodes.set(delta.id, prior ?? {
        id: delta.id,
        lifecycle: delta.lifecycle,
        parentId,
      });
      continue;
    }

    if (prev === null) {
      // First appearance: classify from the entry lifecycle directly. We must
      // NOT call transitionForLifecycleChange("generated", …) because that is
      // idempotent for “generated→generated” and would drop the very spawn
      // event (AC1 “新任务从父问题产生”).
      nodes.set(delta.id, {
        id: delta.id,
        lifecycle: delta.lifecycle,
        parentId,
      });
      const a = appearanceEventFor(delta.lifecycle, parentId);
      if (a) {
        events.push({
          transition_kind: a.kind,
          related_ids: [delta.id],
          anchor_id: a.anchorId,
          status: lifecycleStatusLabel(delta.lifecycle),
        });
      }
      continue;
    }

    const t = transitionForLifecycleChange(prev, delta.lifecycle, parentId);
    nodes.set(delta.id, {
      id: delta.id,
      lifecycle: delta.lifecycle,
      parentId,
    });
    if (!t) continue;
    events.push({
      transition_kind: t.kind,
      related_ids: [delta.id],
      anchor_id: t.anchorId,
      status: lifecycleStatusLabel(delta.lifecycle),
    });
  }

  return { state: { ...model, nodes }, events };
}

/** Stable static status label (preserved under reduced motion, Rule ④). */
export function lifecycleStatusLabel(
  lifecycle: ResearchNodeLifecycle,
): string {
  switch (lifecycle) {
    case "generated":
      return "已生成";
    case "dispatched":
      return "已派工";
    case "succeeded":
      return "已入账";
    case "failed":
      return "待重试";
  }
}

// ─── Read helpers ───────────────────────────────────────────────────────────

/** Node lifecycle snapshot for a given id (for tests / consumers). */
export function nodeLifecycleAt(
  model: ResearchNodeStateModel,
  id: string,
): ResearchNodeLifecycle | null {
  return model.nodes.get(id)?.lifecycle ?? null;
}

/** Number of distinct tracked nodes. */
export function trackedNodeCount(model: ResearchNodeStateModel): number {
  return model.nodes.size;
}
