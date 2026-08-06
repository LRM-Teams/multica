// @vitest-environment node
import { describe, expect, it } from "vitest";
import { classifySemanticLOD, classifyRouteOutcome, isBlockingStatus } from "./classify";
import type { SemanticContext } from "./model";

function ctx(overrides: Partial<SemanticContext> = {}): SemanticContext {
  return {
    selectedId: null,
    ancestorIds: [],
    blockingIds: [],
    runningIds: [],
    pinnedIds: [],
    transitionRootIds: [],
    zoomPct: 100,
    depthById: new Map(),
    explicitThirdLevel: false,
    ...overrides,
  };
}

describe("classifyRouteOutcome (explicit status registry, §3)", () => {
  it("maps known statuses to route outcomes", () => {
    expect(classifyRouteOutcome("running")).toBe("exploring");
    expect(classifyRouteOutcome("accepted")).toBe("accepted");
    expect(classifyRouteOutcome("failed")).toBe("failed");
    expect(classifyRouteOutcome("cancelled")).toBe("cancelled");
    expect(classifyRouteOutcome("stale")).toBe("stale");
    expect(classifyRouteOutcome("disputed")).toBe("disputed");
  });

  it("unknown status / missing always falls to neutral — never guessed", () => {
    expect(classifyRouteOutcome("totally_unknown_thing")).toBe("neutral");
    expect(classifyRouteOutcome(null)).toBe("neutral");
    expect(classifyRouteOutcome(undefined)).toBe("neutral");
    expect(classifyRouteOutcome("")).toBe("neutral");
  });

  it("is case-insensitive and trims whitespace", () => {
    expect(classifyRouteOutcome("  FAILED ")).toBe("failed");
  });

  it("does NOT infer from prose or title text", () => {
    expect(classifyRouteOutcome("这题失败了")).toBe("neutral");
  });

  it("isBlockingStatus covers failed/cancelled/stale", () => {
    expect(isBlockingStatus("failed")).toBe(true);
    expect(isBlockingStatus("cancelled")).toBe(true);
    expect(isBlockingStatus("stale")).toBe(true);
    expect(isBlockingStatus("accepted")).toBe(false);
    expect(isBlockingStatus("running")).toBe(false);
  });
});

describe("classifySemanticLOD", () => {
  const base = {
    kind: "task",
    status: "done",
    importance: 0.5,
  };

  it("promotes selected / ancestor / blocking to landmark regardless of depth", () => {
    for (const overrides of [
      { selectedId: "n1" },
      { ancestorIds: ["n1"] },
      { blockingIds: ["n1"] },
    ]) {
      const res = classifySemanticLOD({
        ...base,
        id: "n1",
        context: ctx({ ...overrides, depthById: new Map([["n1", 20]]) }),
      });
      expect(res.lod).toBe("landmark");
      expect(res.protected).toBe(true);
    }
  });

  it("default 2-layer: depth 2 visible, depth 3 folds unless explicit expand", () => {
    const c = ctx({ depthById: new Map([["n3", 3]]) });
    expect(
      classifySemanticLOD({ ...base, id: "n3", context: c }).lod,
    ).toBe("route-bundle");

    const expanded = ctx({
      depthById: new Map([["n3", 3]]),
      explicitThirdLevel: true,
    });
    expect(
      classifySemanticLOD({ ...base, id: "n3", context: expanded }).lod,
    ).not.toBe("route-bundle");
  });

  it("4th layer always folds into a Route Bundle unless protected", () => {
    const c = ctx({
      depthById: new Map([["n4", 4]]),
      explicitThirdLevel: true, // even explicit expand cannot show 4th layer
    });
    expect(classifySemanticLOD({ ...base, id: "n4", context: c }).lod).toBe(
      "route-bundle",
    );
  });

  it("overview zoom (<35%) demotes non-narrative nodes to trail dots", () => {
    const c = ctx({
      zoomPct: 25,
      depthById: new Map([["n1", 1]]),
    });
    const res = classifySemanticLOD({
      id: "n1",
      kind: "observation",
      status: "active",
      importance: 0.1,
      context: c,
    });
    expect(res.lod).toBe("trail-dot");
  });

  it("overview zoom keeps top Insight / Decision as landmarks", () => {
    const c = ctx({ zoomPct: 25, depthById: new Map([["i1", 2]]) });
    const insight = classifySemanticLOD({
      id: "i1",
      kind: "insight",
      status: "accepted",
      importance: 0.9,
      context: c,
    });
    expect(insight.lod).toBe("landmark");

    const decision = classifySemanticLOD({
      id: "d1",
      kind: "decision",
      status: "resolved",
      importance: 0.8,
      context: ctx({ zoomPct: 25, depthById: new Map([["d1", 2]]) }),
    });
    expect(decision.lod).toBe("landmark");
  });

  it("route zoom keeps medium nodes as waypoints", () => {
    const c = ctx({
      zoomPct: 50,
      depthById: new Map([["q1", 2]]),
    });
    const res = classifySemanticLOD({
      id: "q1",
      kind: "question",
      status: "active",
      importance: 0.4,
      context: c,
    });
    expect(res.lod).toBe("waypoint");
  });

  it("low-importance leaf nodes become trail dots when not waypoint kinds", () => {
    const c = ctx({
      zoomPct: 100,
      depthById: new Map([["s1", 2]]),
    });
    const res = classifySemanticLOD({
      id: "s1",
      kind: "source_candidate",
      status: "active",
      importance: 0.05,
      context: c,
    });
    expect(res.lod).toBe("trail-dot");
  });
});
