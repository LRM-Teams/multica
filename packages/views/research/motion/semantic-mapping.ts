/**
 * LRM-1477 — Semantic transition kind → display mapping.
 *
 * Spec: §2 (transition_kind → unified syntax), Rule ①: the mapping is a
 * `Record<SemanticTransitionKind, SemanticDisplayGroup>` so a new kind that is
 * not in the table fails to compile instead of silently not displaying.
 *
 * All display verbs only ever move `transform` + `opacity` + a static
 * `status`/`highlight` marker — they never change saved layout coordinates,
 * consistent with the trajectory-motion hard rule.
 */

import type { MotionProfile } from "./transition-queue";

/** The 10 projection transition kinds (spec §5.1). */
export type SemanticTransitionKind =
  | "branch_spawned"
  | "task_dispatched"
  | "result_accepted"
  | "integration_formed"
  | "insight_staled"
  | "dispute_opened"
  | "deliberation_progressed"
  | "lead_escalated"
  | "team_membership_changed"
  | "report_revised";

/** Unified display verb (spec §2.2). */
export type DisplayVerb =
  | "appear"
  | "merge"
  | "conflict"
  | "escalate"
  | "stale"
  | "revise"
  | "reappear"
  | "camera";

/**
 * The four semantic super-categories plus the special reconnect/camera classes.
 * The super-category decides the animation signature (diffuse / fuse / clash /
 * rise / stale / revise); the individual kind decides the trigger object and
 * extra markers (spec §2.1).
 */
export type SemanticDisplayGroup =
  | "advance" // appear / progress (result accepted, revise pulse)
  | "appear" // diffuse appear (branch spawn, task dispatch, membership)
  | "merge" // inward converge-thin (integration formed)
  | "stale" // fade-to-grey propagation (insight staled)
  | "conflict" // lateral pull-apart + warning (dispute opened)
  | "escalate" // rise + emphasis (deliberation progressed, lead escalated)
  | "reappear" // reconnect backfill, no historical replay
  | "camera"; // camera-sync (the only verb allowed to move layout)

/**
 * Static end-state markers. The 4 persistent verbs (conflict / escalate /
 * stale / revise) must KEEP a static marker after animation so "conflict",
 * "escalation" and "stale" remain distinguishable at rest (Rule ②).
 */
export type StaticMarker =
  | "conflict-border"
  | "escalate-emphasis"
  | "stale-grey"
  | "revise-pulse"
  | "accepted-check"
  | "exec-badge"
  | "membership"
  | "none";

export type SemanticDisplaySpec = {
  group: SemanticDisplayGroup;
  verb: DisplayVerb;
  /** Static marker that must remain after animation ends (Rule ②). */
  marker: StaticMarker;
  /** Optional hint for the trigger object, from the kind (spec §2.1). */
  labeled?: string;
};

/**
 * Single-structure mapping (Rule ①). Any new kind not listed here is a
 * TypeScript error and must be triaged before display falls to a silent path.
 */
export const SEMANTIC_TRANSITION_KIND_MAP: Record<
  SemanticTransitionKind,
  SemanticDisplaySpec
> = {
  branch_spawned: {
    group: "appear",
    verb: "appear",
    marker: "none",
    labeled: "branch",
  },
  task_dispatched: {
    group: "appear",
    verb: "appear",
    marker: "exec-badge",
    labeled: "task",
  },
  result_accepted: {
    group: "advance",
    verb: "appear",
    marker: "accepted-check",
    labeled: "result",
  },
  integration_formed: {
    group: "merge",
    verb: "merge",
    marker: "none",
    labeled: "insight",
  },
  insight_staled: {
    group: "stale",
    verb: "stale",
    marker: "stale-grey",
    labeled: "insight",
  },
  dispute_opened: {
    group: "conflict",
    verb: "conflict",
    marker: "conflict-border",
    labeled: "claim",
  },
  deliberation_progressed: {
    group: "escalate",
    verb: "escalate",
    marker: "escalate-emphasis",
    labeled: "deliberation",
  },
  lead_escalated: {
    group: "escalate",
    verb: "escalate",
    marker: "escalate-emphasis",
    labeled: "director",
  },
  team_membership_changed: {
    group: "appear",
    verb: "appear",
    marker: "membership",
    labeled: "member",
  },
  report_revised: {
    group: "advance",
    verb: "revise",
    marker: "revise-pulse",
    labeled: "report",
  },
};

/**
 * Resolve a projection transition event to its display spec.
 * Non-exhaustive by construction — because the map is a complete
 * `Record<SemanticTransitionKind, ...>`, an unknown kind cannot compile here.
 */
export function resolveSemanticDisplay(
  kind: SemanticTransitionKind,
): SemanticDisplaySpec {
  return SEMANTIC_TRANSITION_KIND_MAP[kind];
}

/**
 * Effective verb under a motion profile.
 *
 * Reduced motion (Rule ④) collapses every displacement verb to a uniform
 * fade-in (`reappear`) with instant layout — static markers are preserved by
 * the caller via the returned spec. Low-performance keeps the verb but degrades
 * the presentation (handled in directives and the hook).
 */
export function effectiveVerb(
  spec: SemanticDisplaySpec,
  profile: MotionProfile,
): DisplayVerb {
  if (profile.reducedMotion) {
    // "camera" is the only verb that moves layout; under reduce it must not.
    return "reappear";
  }
  return spec.verb;
}
