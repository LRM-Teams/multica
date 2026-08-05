import { describe, expect, it } from "vitest";
import type { ResearchGraphNode } from "@multica/core/types";
import { isAbandonedStatus, readAbandonReason } from "./abandon-reason";

function node(partial: Partial<ResearchGraphNode>): ResearchGraphNode {
  return {
    id: "n1",
    session_id: "s1",
    node_type: "finding",
    title: "定价支",
    summary: "",
    status: "done",
    actor_agent_id: null,
    payload: {},
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    ...partial,
  };
}

describe("isAbandonedStatus", () => {
  it("matches only exact abandoned (case-insensitive)", () => {
    expect(isAbandonedStatus("abandoned")).toBe(true);
    expect(isAbandonedStatus("Abandoned")).toBe(true);
    expect(isAbandonedStatus("cancelled")).toBe(false);
    expect(isAbandonedStatus("done")).toBe(false);
    expect(isAbandonedStatus("")).toBe(false);
  });
});

describe("readAbandonReason", () => {
  it("prefers top-level abandon_reason projection", () => {
    expect(
      readAbandonReason(
        node({
          status: "abandoned",
          abandon_reason: "方向不符",
          payload: { abandon_reason: "payload 不应优先", reason: "质量原因" },
        }),
      ),
    ).toBe("方向不符");
  });

  it("reads payload abandon_reason then deprecate_reason", () => {
    expect(
      readAbandonReason(
        node({ status: "abandoned", payload: { abandon_reason: "用户改需求" } }),
      ),
    ).toBe("用户改需求");
    expect(
      readAbandonReason(
        node({ status: "abandoned", payload: { deprecate_reason: "别名" } }),
      ),
    ).toBe("别名");
  });

  it("never falls back to reason / dead_end_reason / assessment", () => {
    expect(
      readAbandonReason(
        node({
          status: "abandoned",
          assessment: "detour",
          reason: "弯路质量原因",
          payload: { reason: "质量", dead_end_reason: "死胡同" },
        }),
      ),
    ).toBeNull();
  });

  it("treats blank strings as missing", () => {
    expect(
      readAbandonReason(node({ abandon_reason: "   ", payload: { abandon_reason: "" } })),
    ).toBeNull();
  });
});
