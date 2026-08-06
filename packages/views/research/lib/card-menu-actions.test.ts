// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchGraphNode } from "@multica/core/types";
import { cardMenuItemsForNode } from "./card-menu-actions";

function node(partial: Partial<ResearchGraphNode> & Pick<ResearchGraphNode, "id" | "title">): ResearchGraphNode {
  return {
    session_id: "s1",
    node_type: "probe",
    summary: "",
    status: "done",
    actor_agent_id: null,
    payload: {},
    created_at: "2026-08-03T00:00:00Z",
    updated_at: "2026-08-03T00:00:00Z",
    ...partial,
  };
}

describe("cardMenuItemsForNode", () => {
  it("wires retry only when failed; missing APIs stay disabled with reasons", () => {
    const failed = node({ id: "f", title: "F", status: "failed", node_type: "dead_end" });
    const ok = node({ id: "o", title: "O", status: "done" });
    const failItems = cardMenuItemsForNode(failed);
    const okItems = cardMenuItemsForNode(ok);
    expect(failItems.find((i) => i.id === "retry_failed")?.enabled).toBe(true);
    expect(okItems.find((i) => i.id === "retry_failed")?.enabled).toBe(false);
    expect(failItems.find((i) => i.id === "fork_from")?.disabledReason).toMatch(
      /not available|不可创建探索分支/i,
    );
    expect(failItems.find((i) => i.id === "reassign")?.enabled).toBe(false);
    expect(failItems.find((i) => i.id === "cancel_run")?.enabled).toBe(false);
  });
});
