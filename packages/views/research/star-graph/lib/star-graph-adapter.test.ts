import { describe, expect, it } from "vitest";

import {
  mapMetrics,
  mapNodeState,
  resolveTier,
  toStarGraphNodeView,
} from "./star-graph-adapter";
import type { StarGraphNodeInput } from "./star-graph-contract";

function node(partial: Partial<StarGraphNodeInput>): StarGraphNodeInput {
  return {
    id: "n1",
    node_kind: "claim",
    status: "done",
    importance: 2,
    title: "定价支撑充分",
    ...partial,
  };
}

describe("resolveTier — degradation chain (red line: never fabricate)", () => {
  it("uses the typed LRM-1505 level when present", () => {
    const r = resolveTier(node({ typed: { level: "xxl" } }));
    expect(r.tier).toBe("xxl");
    expect(r.source).toBe("typed");
  });

  it("degrades a claim (cognition) to m when importance is not strong", () => {
    const r = resolveTier(node({ node_kind: "claim", importance: 1 }));
    expect(r.tier).toBe("m");
    expect(r.source).toBe("kind-classified");
  });

  it("promotes a strongly important conclusion to xl", () => {
    const r = resolveTier(node({ node_kind: "insight", importance: 3 }));
    expect(r.tier).toBe("xl");
  });

  it("maps an execution action to the S agent dot", () => {
    const r = resolveTier(node({ node_kind: "attempt", importance: 1 }));
    expect(r.tier).toBe("s");
  });

  it("maps the episode umbrella to the top tier when strong", () => {
    const r = resolveTier(node({ node_kind: "episode", importance: 3 }));
    expect(r.tier).toBe("xxl");
  });

  it("falls back to safe mid tier for an unknown future kind (no crash, no fake)", () => {
    const r = resolveTier(node({ node_kind: "some_future_kind", importance: 5 }));
    expect(r.tier).toBe("m");
    expect(r.source).toBe("fallback");
  });
});

describe("mapNodeState — real status strings", () => {
  it("maps running to run", () => {
    expect(mapNodeState("running")).toBe("run");
  });
  it("maps pending_review to pending-review", () => {
    expect(mapNodeState("pending_review")).toBe("pending-review");
  });
  it("maps every canonical successful projection status to stable", () => {
    expect(mapNodeState("done")).toBe("stable");
    expect(mapNodeState("terminal")).toBe("stable");
    expect(mapNodeState("succeeded")).toBe("stable");
    expect(mapNodeState("resolved")).toBe("stable");
  });
  it("maps failed to failed", () => {
    expect(mapNodeState("failed")).toBe("failed");
  });
  it("defaults unknown statuses to default", () => {
    expect(mapNodeState("weird_unknown")).toBe("default");
  });
});

describe("mapMetrics — never synthesised", () => {
  it("returns undefined when no typed fields exist", () => {
    expect(mapMetrics(undefined)).toBeUndefined();
  });

  it("only emits present fields", () => {
    expect(mapMetrics({ document_count: 12, round: 2 })).toEqual({
      documentCount: 12,
      round: "2",
    });
  });

  it("converts canonical 0..1 confidence to percent and ignores invalid counts", () => {
    expect(mapMetrics({ confidence: 0.87, document_count: -1 })).toEqual({
      confidence: 87,
    });
  });

  it("preserves an already-percent confidence and omits null or invalid values", () => {
    expect(mapMetrics({ confidence: 84 })).toEqual({ confidence: 84 });
    expect(mapMetrics({ confidence: null })).toBeUndefined();
    expect(mapMetrics({ confidence: Number.NaN })).toBeUndefined();
  });
});

describe("toStarGraphNodeView — full view", () => {
  it("produces presentation props for an S-tier agent with real badge", () => {
    const v = toStarGraphNodeView(
      node({
        node_kind: "attempt",
        status: "running",
        actor_agent_id: "agent:lindberg",
        title: "核验来源",
        typed: { confidence: 0.5 },
      }),
    );
    expect(v.tier).toBe("s");
    expect(v.state).toBe("run");
    expect(v.agentBadge).toBe("LIN");
    expect(v.headerLabel).toBeUndefined();
  });

  it("renders an active S-tier execution node as running without pulsing results", () => {
    expect(
      toStarGraphNodeView(node({ node_kind: "attempt", status: "active" })).state,
    ).toBe("run");
    expect(
      toStarGraphNodeView(node({ node_kind: "claim", status: "active" })).state,
    ).toBe("default");
  });

  it("emits an accessible header label for non-S tiers", () => {
    const v = toStarGraphNodeView(node({ node_kind: "claim", importance: 3 }));
    expect(v.tier).toBe("xl");
    expect(v.headerLabel).toBe("稳定结论");
  });

  it("shows a projection summary on result tiers without inventing one", () => {
    const withSummary = toStarGraphNodeView(
      node({
        node_kind: "insight",
        importance: 3,
        summary: "专业自治与阶段质量门共同降低同步成本",
      }),
    );
    const withoutSummary = toStarGraphNodeView(
      node({ node_kind: "insight", importance: 3 }),
    );

    expect(withSummary.subLabel).toBe("专业自治与阶段质量门共同降低同步成本");
    expect(withoutSummary.subLabel).toBeUndefined();
  });

  it("omits agent badge when the projection carries no agent id", () => {
    const v = toStarGraphNodeView(node({ node_kind: "attempt", actor_agent_id: null }));
    expect(v.agentBadge).toBeUndefined();
  });
});
