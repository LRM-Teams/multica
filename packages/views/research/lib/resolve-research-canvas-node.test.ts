import { describe, expect, it } from "vitest";
import {
  mergeResearchCanvasNodes,
  resolveResearchCanvasNode,
  typedNodeToSnapshotNode,
  enrichResearchNodeForDetail,
} from "./resolve-research-canvas-node";
import type { ResearchGraphNode } from "@multica/core/types";

describe("resolveResearchCanvasNode", () => {
  const snapshotNode = {
    id: "snap-1",
    session_id: "s1",
    node_type: "finding",
    title: "From snapshot",
    summary: "snap summary",
    status: "done",
    actor_agent_id: null,
    payload: {},
    created_at: "",
    updated_at: "",
  } satisfies ResearchGraphNode;

  it("prefers snapshot over typed when both exist", () => {
    const resolved = resolveResearchCanvasNode("snap-1", {
      snapshotNodes: [snapshotNode],
      typedGraph: {
	        nodes: [{ id: "snap-1", title: "From typed", node_type: "finding" }],
      },
    });
    expect(resolved?.title).toBe("From snapshot");
  });

  it("falls back to typed when snapshot is missing", () => {
    const resolved = resolveResearchCanvasNode("typed-only", {
      snapshotNodes: [],
      typedGraph: {
        nodes: [
          {
            id: "typed-only",
            title: "Typed node",
            node_type: "probe",
            status: "running",
	          },
        ],
      },
    });
    expect(resolved?.title).toBe("Typed node");
    expect(resolved?.node_type).toBe("probe");
  });

  it("mergeResearchCanvasNodes unions typed-only ids", () => {
    const merged = mergeResearchCanvasNodes([snapshotNode], {
	      nodes: [{ id: "typed-only", title: "Typed only", node_type: "finding" }],
    });
    expect(merged.map((node) => node.id).toSorted()).toEqual(["snap-1", "typed-only"]);
  });

  it("typedNodeToSnapshotNode passes payload through", () => {
    const node = typedNodeToSnapshotNode({
      id: "n1",
      node_type: "finding",
      payload: { dimension_family: "market" },
	    });
    expect(node.payload).toEqual({ dimension_family: "market" });
  });

  it("enrichResearchNodeForDetail merges typed payload over snapshot", () => {
    const snapshotNode = {
      id: "n1",
      session_id: "s1",
      node_type: "finding",
      title: "Snap title",
      summary: "snap summary only",
      status: "done",
      actor_agent_id: null,
      payload: {},
      created_at: "",
      updated_at: "",
    } satisfies ResearchGraphNode;
    const enriched = enrichResearchNodeForDetail(snapshotNode, {
      nodes: [
        {
          id: "n1",
          node_type: "finding",
          title: "Typed title",
          payload: { details: { result: "from backend" } },
	        },
      ],
    });
    expect(enriched.payload).toEqual({ details: { result: "from backend" } });
    expect(enriched.title).toBe("Typed title");
  });
});
