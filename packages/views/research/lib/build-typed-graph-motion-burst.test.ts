import { describe, expect, it } from "vitest";
import type { TypedGraphResponse } from "@multica/core/research";
import {
  createTransitionQueue,
  liveQueueSize,
  transitionQueueReducer,
} from "../motion/transition-queue";
import { MOTION_GLOW_MAX_ACTIVE, MOTION_QUEUE_CAP } from "../motion/tokens";
import { capTransitionGlowDirectives } from "../motion/glow-budget";
import type { MotionDirective } from "../motion/directives";
import { buildTypedGraphMotionEvents } from "./build-typed-graph-motion-events";

const emptyLineage = {
  derived: {},
  merged: {},
  superseded: {},
  restarted: {},
  invalidated: {},
  supersedes: {},
};

function graph(version: number, nodeCount: number): TypedGraphResponse {
  return {
    session_id: "s1",
    graph_version: version,
    nodes: Array.from({ length: nodeCount }, (_, index) => ({
      id: `n${index}`,
      title: `Node ${index}`,
      status: "running",
      level: "s",
    })),
    edges: [],
    clusters: [],
    lineage: emptyLineage,
  } as unknown as TypedGraphResponse;
}

function glowDirective(id: string): MotionDirective {
  return {
    className: `motion-${id}`,
    style: {},
    markerClass: null,
    dataVerb: "appear",
    glowDisabled: false,
  };
}

describe("D5 slice G — typed graph motion burst gate", () => {
  it("100 sequential typed-graph deltas stay within the motion queue cap", () => {
    let previous = graph(0, 1);
    let queue = createTransitionQueue({ nowMs: 0 });
    let peak = 0;

    for (let version = 1; version <= 100; version += 1) {
      const next = graph(version, version + 1);
      const events = buildTypedGraphMotionEvents(previous, next);
      expect(events.length).toBeGreaterThan(0);
      for (const event of events) {
        queue = transitionQueueReducer(queue, {
          type: "enqueue",
          event,
          nowMs: version * 16,
        });
        peak = Math.max(peak, liveQueueSize(queue));
      }
      previous = next;
    }

    expect(peak).toBeLessThanOrEqual(MOTION_QUEUE_CAP);
  });

  it("caps concurrent transition glow when many entities animate at once", () => {
    const directives = new Map<string, MotionDirective | null>();
    for (let index = 0; index < 100; index += 1) {
      directives.set(`n${index}`, glowDirective(`n${index}`));
    }

    const capped = capTransitionGlowDirectives(directives);
    const activeGlow = [...capped.values()].filter(
      (directive) => directive && !directive.glowDisabled,
    ).length;

    expect(activeGlow).toBeLessThanOrEqual(MOTION_GLOW_MAX_ACTIVE);
  });
});
