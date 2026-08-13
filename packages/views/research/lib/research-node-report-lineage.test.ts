import { describe, expect, it } from "vitest";
import type { TypedGraphNode } from "@multica/core/research";
import type { ResearchGraphNode } from "@multica/core/types";
import { buildNodeReportLineage } from "./research-node-report-lineage";

describe("buildNodeReportLineage", () => {
  it("preserves every canonical typed lineage family", () => {
    const node = {
      id: "result",
      derived_from: "draft",
      merged_from: ["input-a", "input-b"],
      parent_id: "direction",
      child_ids: ["child"],
      children_of: ["synthesis"],
      restart_of: "failed-attempt",
      superseded_by: "result-v2",
      invalidated_by: "counterevidence",
    } as TypedGraphNode;

    expect(buildNodeReportLineage(node, null)).toEqual([
      { relation: "derived_from", nodeIds: ["draft"] },
      { relation: "merged_from", nodeIds: ["input-a", "input-b"] },
      { relation: "parent", nodeIds: ["direction"] },
      { relation: "children", nodeIds: ["child"] },
      { relation: "used_by", nodeIds: ["synthesis"] },
      { relation: "restart_of", nodeIds: ["failed-attempt"] },
      { relation: "superseded_by", nodeIds: ["result-v2"] },
      { relation: "invalidated_by", nodeIds: ["counterevidence"] },
    ]);
  });

  it("uses the legacy snapshot merge fact only when typed merge inputs are absent", () => {
    expect(
      buildNodeReportLineage(
        { id: "result", merged_from: [] } as unknown as TypedGraphNode,
        {
          id: "result",
          payload: { merged_from: ["legacy-a", "legacy-a", "result", 3] },
        } as ResearchGraphNode,
      ),
    ).toEqual([{ relation: "merged_from", nodeIds: ["legacy-a"] }]);
  });
});
