/**
 * LRM-1477 — Contract fixture for projection deltas.
 *
 * Spec: §5.1 (interface not yet landed → use strict-following contract
 * fixtures), §8.1 (demo/backpressure fixtures). Production paths must not keep
 * fabricated data — this module is demo/test only, matching the existing
 * `trajectory-performance-fixture` convention.
 */

import type { ProjectionTransitionEvent } from "./transition-queue";
import type { SemanticTransitionKind } from "./semantic-mapping";

export const ALL_TRANSITION_KINDS: SemanticTransitionKind[] = [
  "branch_spawned",
  "task_dispatched",
  "result_accepted",
  "integration_formed",
  "insight_staled",
  "dispute_opened",
  "deliberation_progressed",
  "lead_escalated",
  "team_membership_changed",
  "report_revised",
];

/** One representative event per kind, each with stable related ids. */
export function semanticTransitionFixture(
  prefix = "node",
): ProjectionTransitionEvent[] {
  return ALL_TRANSITION_KINDS.map((kind, index) => ({
    transition_kind: kind,
    related_ids: [`${prefix}-${kind}-${index}`],
    anchor_id: index > 0 ? `${prefix}-anchor-${index}` : null,
    status: "active",
  }));
}

/** Build a single event for the given kind. */
export function transitionEvent(
  kind: SemanticTransitionKind,
  overrides: Partial<ProjectionTransitionEvent> = {},
): ProjectionTransitionEvent {
  return {
    transition_kind: kind,
    related_ids: [`node-${kind}`],
    anchor_id: null,
    status: "active",
    ...overrides,
  };
}

/**
 * A 100-delta burst mixing all 10 kinds with repeats (spec §5.4 Rule ⑦): a
 * mix of same-lane coalescing candidates and distinct lanes, including a
 * hidden-period window for the background-restore path.
 */
export function hundredDeltaBurst(
  overrides: { hiddenCount?: number } = {},
): ProjectionTransitionEvent[] {
  const hiddenCount = overrides.hiddenCount ?? 0;
  const events: ProjectionTransitionEvent[] = [];
  for (let i = 0; i < 100; i += 1) {
    const kind = ALL_TRANSITION_KINDS[i % ALL_TRANSITION_KINDS.length];
    // Repeat within a small budget window to exercise coalescing; distinct
    // anchor for index multiples of 7 to widen distinct lanes.
    const anchor = i % 7 === 0 ? `anchor-${i % 5}` : `anchor-shared`;
    events.push({
      transition_kind: kind,
      related_ids: [`n-${i}`, `n-${i + 1000}`],
      anchor_id: anchor,
      status: i % 3 === 0 ? "active" : "pending",
    });
  }
  return events;
}
