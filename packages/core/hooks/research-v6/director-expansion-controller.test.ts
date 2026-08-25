import { QueryClient } from "@tanstack/react-query";
import { beforeEach, describe, expect, it } from "vitest";
import type {
  ResearchV6DirectorProjectionSnapshot,
  ResearchV6DirectorProjectionTransport,
} from "../../types/research-v6-director";
import { useResearchV6DirectorDisplayStore } from "./director-display-store";
import { ResearchV6DirectorExpansionController } from "./director-expansion-controller";

const WORKSPACE_ID = "00000000-0000-4000-8000-000000000001";
const RUN_ID = "00000000-0000-4000-8000-000000000003";
const SNAPSHOT_ID = "00000000-0000-4000-8000-000000000601";
const HASH = `sha256:${"d".repeat(64)}`;

function page(
  nodeIds: string[],
  options: { hasMore?: boolean; nextCursor?: string } = {},
): ResearchV6DirectorProjectionSnapshot {
  return {
    contractKind: "projection_snapshot",
    schemaVersion: 6,
    snapshotId: SNAPSHOT_ID,
    workspaceId: WORKSPACE_ID,
    runId: RUN_ID,
    throughEventSequence: 4,
    projectionHash: HASH,
    sliceKey: "expand:root",
    nodes: nodeIds.map((id) => ({
      id,
      kind: "result_s",
      tier: "S",
      canonicalRef: { kind: "result", id: RUN_ID },
      branchIds: [],
      state: {
        execution: "succeeded",
        conclusion: "accepted",
        integration: "absorbed",
      },
      catalogSummary: id,
      absorbed: true,
      terminal: true,
      expandable: false,
      hiddenChildCount: 0,
      updatedAt: "2026-08-17T08:00:00Z",
    })),
    edges: [],
    densityBins: [],
    hasMore: options.hasMore ?? false,
    nextCursor: options.nextCursor,
  };
}

function transport(pages: ResearchV6DirectorProjectionSnapshot[]) {
  let index = 0;
  return {
    loadSlice: async () => pages[index++]!,
  } as Pick<ResearchV6DirectorProjectionTransport, "loadSlice"> as ResearchV6DirectorProjectionTransport;
}

describe("ResearchV6DirectorExpansionController", () => {
  beforeEach(() => useResearchV6DirectorDisplayStore.getState().clear());

  it("loads and commits only the server-returned direct layer", async () => {
    const controller = new ResearchV6DirectorExpansionController(
      new QueryClient(),
      transport([page(["root", "child-a", "child-b"])]),
      { workspaceId: WORKSPACE_ID, runId: RUN_ID, snapshotId: SNAPSHOT_ID },
    );
    await controller.toggle("root", "failed");
    expect(
      useResearchV6DirectorDisplayStore.getState().expandedByRoot.root
        ?.revealedNodeIds,
    ).toEqual(["child-a", "child-b"]);
  });

  it("paginates explicitly and animates only newly revealed nodes", async () => {
    const controller = new ResearchV6DirectorExpansionController(
      new QueryClient(),
      transport([
        page(["root", "child-a"], { hasMore: true, nextCursor: "p2" }),
        page(["child-b"]),
      ]),
      { workspaceId: WORKSPACE_ID, runId: RUN_ID, snapshotId: SNAPSHOT_ID },
    );
    await controller.toggle("root", "failed");
    await controller.loadMore("root", "failed");
    const state = useResearchV6DirectorDisplayStore.getState();
    expect(state.expandedByRoot.root?.revealedNodeIds).toEqual([
      "child-a",
      "child-b",
    ]);
    expect(state.transition?.revealedNodeIds).toEqual(["child-b"]);
  });

  it("keeps canonical data cached when the user collapses presentation", async () => {
    const queryClient = new QueryClient();
    const controller = new ResearchV6DirectorExpansionController(
      queryClient,
      transport([page(["child"])]),
      { workspaceId: WORKSPACE_ID, runId: RUN_ID, snapshotId: SNAPSHOT_ID },
    );
    await controller.toggle("root", "failed");
    controller.collapse("root");
    expect(useResearchV6DirectorDisplayStore.getState().expandedByRoot).toEqual(
      {},
    );
    expect(queryClient.getQueryCache().getAll()).toHaveLength(1);
  });
});
