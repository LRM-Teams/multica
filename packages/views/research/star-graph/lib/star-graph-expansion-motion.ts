import type { MotionDirective } from "../../motion/directives";
import {
  MOTION_EASING,
  MOTION_MERGE_MS,
  MOTION_STAGGER_CAP,
  MOTION_STAGGER_MS,
} from "../../motion/tokens";
import type { StarCanvasViewModel } from "./star-canvas-view-model";
import type { StarGraphExpansionTransition } from "./star-graph-expansion";

export function buildStarGraphExpansionMotion(
  model: StarCanvasViewModel,
  transition: StarGraphExpansionTransition | null | undefined,
  lowPerformance = false,
): ReadonlyMap<string, MotionDirective> {
  const directives = new Map<string, MotionDirective>();
  if (!transition) return directives;

  const root = model.entities.find(
    (entity) => entity.id === transition.rootNodeId,
  );
  if (!root) return directives;

  const transactionClass = `txn-expansion-${transition.sequence}`;
  if (transition.kind === "collapse") {
    directives.set(root.id, {
      className: `research-motion-expansion-collapse ${transactionClass}`,
      style: {
        animationName: "research-motion-expansion-collapse",
        animationDuration: `${MOTION_MERGE_MS}ms`,
        animationTimingFunction: MOTION_EASING,
      },
      markerClass: null,
      dataVerb: "merge",
      glowDisabled: lowPerformance,
    });
    return directives;
  }

  transition.revealedNodeIds.forEach((nodeId, index) => {
    const node = model.entities.find((entity) => entity.id === nodeId);
    if (!node) return;
    directives.set(node.id, {
      className: `research-motion-expansion-reveal ${transactionClass}`,
      style: {
        animationName: "research-motion-expansion-reveal",
        animationDuration: `${MOTION_MERGE_MS}ms`,
        animationTimingFunction: MOTION_EASING,
        animationDelay: `${Math.min(index, MOTION_STAGGER_CAP) * MOTION_STAGGER_MS}ms`,
        "--expansion-origin-x": `${root.x - node.x}px`,
        "--expansion-origin-y": `${root.y - node.y}px`,
        "--expansion-blur": lowPerformance ? "0px" : "5px",
      },
      markerClass: null,
      dataVerb: "appear",
      glowDisabled: lowPerformance,
    });
  });
  return directives;
}

/** Exact relations introduced by this server-declared one-layer expansion. */
export function selectStarGraphExpansionRelationIds(
  model: StarCanvasViewModel,
  transition: StarGraphExpansionTransition | null | undefined,
): ReadonlySet<string> {
  if (!transition || transition.kind !== "expand") return new Set();
  const revealed = new Set(transition.revealedNodeIds);
  const disclosedLayer = new Set([transition.rootNodeId, ...revealed]);
  return new Set(
    model.relations
      .filter(
        (relation) =>
          disclosedLayer.has(relation.fromNodeId) &&
          disclosedLayer.has(relation.toNodeId) &&
          (revealed.has(relation.fromNodeId) || revealed.has(relation.toNodeId)),
      )
      .map((relation) => relation.id),
  );
}
