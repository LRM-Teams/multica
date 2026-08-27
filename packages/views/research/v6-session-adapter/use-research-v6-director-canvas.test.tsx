// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useResearchV6DirectorDisplayStore } from "@multica/core/research-v6/director-display-store";
import type { ResearchV6DirectorRealtimeBus } from "@multica/core/research-v6-live/director-controller";
import type {
  ResearchV6DirectorProjectionSliceRequest,
  ResearchV6DirectorProjectionSnapshot,
  ResearchV6DirectorProjectionTransport,
} from "@multica/core/types/research-v6-director";
import { useResearchV6DirectorCanvas } from "./use-research-v6-director-canvas";

const WORKSPACE_ID = "00000000-0000-4000-8000-000000000001";
const RUN_ID = "00000000-0000-4000-8000-000000000003";
const SNAPSHOT_ID = "00000000-0000-4000-8000-000000000601";

function snapshot(
  sliceKey: string,
  nodes: Array<{ id: string; tier: "S" | "L"; expandable: boolean }>,
  snapshotId = SNAPSHOT_ID,
): ResearchV6DirectorProjectionSnapshot {
  return {
    contractKind: "projection_snapshot",
    schemaVersion: 6,
    snapshotId,
    workspaceId: WORKSPACE_ID,
    runId: RUN_ID,
    throughEventSequence: 4,
    projectionHash: `sha256:${"d".repeat(64)}`,
    sliceKey,
    nodes: nodes.map(({ id, tier, expandable }) => ({
      id,
      kind: tier === "S" ? "result_s" : "insight",
      tier,
      canonicalRef: { kind: tier === "S" ? "result" : "insight", id: RUN_ID },
      branchIds: [],
      state: {
        execution: "succeeded",
        conclusion: "accepted",
        integration: tier === "S" ? "absorbed" : "candidate",
      },
      catalogSummary: id,
      absorbed: tier === "S",
      terminal: true,
      expandable,
      hiddenChildCount: expandable ? 1 : 0,
      updatedAt: "2026-08-17T08:00:00Z",
    })),
    edges: [],
    densityBins: [],
    hasMore: false,
  };
}

function projectionNodeWire(
  node: ResearchV6DirectorProjectionSnapshot["nodes"][number],
) {
  return {
    id: node.id,
    kind: node.kind,
    tier: node.tier,
    canonical_ref: {
      kind: node.canonicalRef.kind,
      id: node.canonicalRef.id,
      revision: node.canonicalRef.revision,
      version_id: node.canonicalRef.versionId,
      content_hash: node.canonicalRef.contentHash,
    },
    branch_ids: node.branchIds,
    state: node.state,
    title: node.title,
    catalog_summary: node.catalogSummary,
    absorbed: node.absorbed,
    terminal: node.terminal,
    expandable: node.expandable,
    hidden_child_count: node.hiddenChildCount,
    updated_at: node.updatedAt,
  };
}

function wrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

describe("useResearchV6DirectorCanvas", () => {
  beforeEach(() => useResearchV6DirectorDisplayStore.getState().clear());

  it("hydrates the shared graph and reveals one server slice on disclosure", async () => {
    const transport = {
      loadSnapshot: async () =>
        snapshot("default", [{ id: "root", tier: "L", expandable: true }]),
      loadSlice: async () =>
        snapshot("expand:root", [{ id: "child", tier: "L", expandable: false }]),
    } as Pick<
      ResearchV6DirectorProjectionTransport,
      "loadSnapshot" | "loadSlice"
    > as ResearchV6DirectorProjectionTransport;
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const { result } = renderHook(
      () =>
        useResearchV6DirectorCanvas({
          workspaceId: WORKSPACE_ID,
          runId: RUN_ID,
          transport,
          expansionFailureLabel: "Expansion failed",
        }),
      { wrapper: wrapper(client) },
    );
    await waitFor(() =>
      expect(result.current.canvas?.graph.nodes).toHaveLength(1),
    );
    act(() => result.current.expansionControl?.onToggleNode("root"));
    await waitFor(() =>
      expect(result.current.canvas?.graph.nodes).toHaveLength(2),
    );
    expect(result.current.expansionControl?.expandedNodeIds.has("root")).toBe(
      true,
    );
  });

  it("keeps an expanded layer visible while rebasing it onto a new snapshot", async () => {
    const nextSnapshotId = "00000000-0000-4000-8000-000000000602";
    let activeSnapshotId = SNAPSHOT_ID;
    const loadedSliceSnapshotIds: string[] = [];
    const transport = {
      loadSnapshot: async () =>
        snapshot(
          "default",
          [{ id: "root", tier: "L", expandable: true }],
          activeSnapshotId,
        ),
      loadSlice: async (
        _workspaceId: string,
        _runId: string,
        request: ResearchV6DirectorProjectionSliceRequest,
      ) => {
        loadedSliceSnapshotIds.push(request.snapshotId);
        return snapshot(
          "expand:root",
          [{ id: "child", tier: "L", expandable: false }],
          request.snapshotId,
        );
      },
    } as Pick<
      ResearchV6DirectorProjectionTransport,
      "loadSnapshot" | "loadSlice"
    > as ResearchV6DirectorProjectionTransport;
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const { result } = renderHook(
      () =>
        useResearchV6DirectorCanvas({
          workspaceId: WORKSPACE_ID,
          runId: RUN_ID,
          transport,
          expansionFailureLabel: "Expansion failed",
        }),
      { wrapper: wrapper(client) },
    );
    await waitFor(() =>
      expect(result.current.canvas?.graph.nodes).toHaveLength(1),
    );
    act(() => result.current.expansionControl?.onToggleNode("root"));
    await waitFor(() =>
      expect(result.current.canvas?.graph.nodes).toHaveLength(2),
    );

    act(() => {
      activeSnapshotId = nextSnapshotId;
      result.current.refetch();
    });

    await waitFor(() => expect(result.current.snapshotId).toBe(nextSnapshotId));
    expect(result.current.canvas?.graph.nodes).toHaveLength(2);
    await waitFor(() =>
      expect(loadedSliceSnapshotIds).toEqual([SNAPSHOT_ID, nextSnapshotId]),
    );
    expect(result.current.expansionControl?.expandedNodeIds.has("root")).toBe(
      true,
    );
    expect(
      useResearchV6DirectorDisplayStore.getState().expandedByRoot.root
        ?.snapshotId,
    ).toBe(nextSnapshotId);
  });

  it("shows a staffing Agent satellite from a live delta without refetching", async () => {
    let snapshotLoads = 0;
    let pushEvent = (_payload: unknown) => {};
    const realtimeBus = {
      subscribeEvent: (_event, handler) => {
        pushEvent = handler;
        return () => {
          pushEvent = () => {};
        };
      },
      onBusReconnect: () => () => {},
      onBusConnectionStatus: () => () => {},
    } satisfies ResearchV6DirectorRealtimeBus;
    const initial: ResearchV6DirectorProjectionSnapshot = {
      ...snapshot("default", []),
      nodes: [
        {
          id: "goal",
          kind: "goal",
          tier: "GOAL",
          canonicalRef: { kind: "goal", id: RUN_ID },
          branchIds: [],
          state: {
            execution: "running",
            conclusion: "accepted",
            integration: "unmatched",
          },
          title: "Research Manus",
          catalogSummary: "Research Manus",
          absorbed: false,
          terminal: false,
          expandable: true,
          hiddenChildCount: 0,
          updatedAt: "2026-08-17T08:00:00Z",
        },
      ],
    };
    const transport = {
      loadSnapshot: async () => {
        snapshotLoads += 1;
        return initial;
      },
    } as Pick<
      ResearchV6DirectorProjectionTransport,
      "loadSnapshot"
    > as ResearchV6DirectorProjectionTransport;
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const { result } = renderHook(
      () =>
        useResearchV6DirectorCanvas({
          workspaceId: WORKSPACE_ID,
          runId: RUN_ID,
          transport,
          realtimeBus,
          expansionFailureLabel: "Expansion failed",
        }),
      { wrapper: wrapper(client) },
    );
    await waitFor(() =>
      expect(result.current.canvas?.graph.nodes.map((node) => node.id)).toEqual([
        "goal",
      ]),
    );
    const agentId = "00000000-0000-4000-8000-000000000221";
    act(() => {
      pushEvent({
        run_id: RUN_ID,
        delta: {
          contract_kind: "projection_delta",
          schema_version: 6,
          workspace_id: WORKSPACE_ID,
          run_id: RUN_ID,
          snapshot_id: SNAPSHOT_ID,
          event_sequence: 5,
          previous_projection_hash: initial.projectionHash,
          projection_hash: `sha256:${"e".repeat(64)}`,
          upsert_nodes: [
            {
              id: "researcher",
              kind: "agent",
              tier: "S",
              canonical_ref: { kind: "agent", id: agentId },
              branch_ids: [],
              state: {
                execution: "idle",
                conclusion: "proposed",
                integration: "unmatched",
              },
              title: "市场研究员",
              catalog_summary: "Cover the market branch",
              absorbed: false,
              terminal: false,
              expandable: false,
              hidden_child_count: 0,
              updated_at: "2026-08-17T08:00:10Z",
            },
          ],
          remove_node_ids: [],
          upsert_edges: [
            {
              id: "researcher-goal",
              kind: "belongs_to",
              from_node_id: "researcher",
              to_node_id: "goal",
              canonical: true,
              hidden_count: 0,
              expandable: false,
            },
          ],
          remove_edge_ids: [],
          invalidate_slice_keys: [],
        },
      });
    });
    await waitFor(() => {
      expect(result.current.canvas?.graph.nodes.map((node) => node.id)).toEqual([
        "goal",
        "researcher",
      ]);
      expect(
        result.current.canvas?.graph.nodes.find((node) => node.id === "researcher"),
      ).toMatchObject({
        node_type: "agent",
        payload: { semantic_role: "roster" },
      });
    });
    expect(snapshotLoads).toBe(1);
  });

  it("renders a committed realtime delta without refetching the snapshot", async () => {
    let pushEvent = (_payload: unknown) => {};
    const realtimeBus = {
      subscribeEvent: (_event, handler) => {
        pushEvent = handler;
        return () => {
          pushEvent = () => {};
        };
      },
      onBusReconnect: () => () => {},
      onBusConnectionStatus: () => () => {},
    } satisfies ResearchV6DirectorRealtimeBus;
    const initial = snapshot("default", [
      { id: "root", tier: "L", expandable: false },
    ]);
    const transport = {
      loadSnapshot: async () => initial,
    } as Pick<
      ResearchV6DirectorProjectionTransport,
      "loadSnapshot"
    > as ResearchV6DirectorProjectionTransport;
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const { result } = renderHook(
      () =>
        useResearchV6DirectorCanvas({
          workspaceId: WORKSPACE_ID,
          runId: RUN_ID,
          transport,
          realtimeBus,
          expansionFailureLabel: "Expansion failed",
        }),
      { wrapper: wrapper(client) },
    );
    await waitFor(() => expect(result.current.canvas?.graph.nodes).toHaveLength(1));
    const nextNode = snapshot("default", [
      { id: "live", tier: "L", expandable: false },
    ]).nodes[0]!;
    act(() => {
      pushEvent({
        run_id: RUN_ID,
        delta: {
          contract_kind: "projection_delta",
          schema_version: 6,
          workspace_id: WORKSPACE_ID,
          run_id: RUN_ID,
          snapshot_id: SNAPSHOT_ID,
          event_sequence: 5,
          previous_projection_hash: initial.projectionHash,
          projection_hash: `sha256:${"e".repeat(64)}`,
          upsert_nodes: [projectionNodeWire(nextNode)],
          remove_node_ids: [],
          upsert_edges: [],
          remove_edge_ids: [],
          invalidate_slice_keys: [],
        },
      });
    });
    await waitFor(() => expect(result.current.canvas?.graph.nodes).toHaveLength(2));
  });

  it("hides a canonically absorbed delta node until its successor is expanded", async () => {
    let pushEvent = (_payload: unknown) => {};
    const realtimeBus = {
      subscribeEvent: (_event, handler) => {
        pushEvent = handler;
        return () => {
          pushEvent = () => {};
        };
      },
      onBusReconnect: () => () => {},
      onBusConnectionStatus: () => () => {},
    } satisfies ResearchV6DirectorRealtimeBus;
    const initial = snapshot("default", [
      { id: "successor", tier: "L", expandable: false },
    ]);
    const absorbed = snapshot("default", [
      { id: "absorbed-s", tier: "S", expandable: false },
    ]).nodes[0]!;
    const transport = {
      loadSnapshot: async () => initial,
    } as Pick<
      ResearchV6DirectorProjectionTransport,
      "loadSnapshot"
    > as ResearchV6DirectorProjectionTransport;
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const { result } = renderHook(
      () =>
        useResearchV6DirectorCanvas({
          workspaceId: WORKSPACE_ID,
          runId: RUN_ID,
          transport,
          realtimeBus,
          expansionFailureLabel: "Expansion failed",
        }),
      { wrapper: wrapper(client) },
    );
    await waitFor(() => expect(result.current.canvas?.graph.nodes).toHaveLength(1));

    act(() => {
      pushEvent({
        run_id: RUN_ID,
        delta: {
          contract_kind: "projection_delta",
          schema_version: 6,
          workspace_id: WORKSPACE_ID,
          run_id: RUN_ID,
          snapshot_id: SNAPSHOT_ID,
          event_sequence: 5,
          previous_projection_hash: initial.projectionHash,
          projection_hash: `sha256:${"e".repeat(64)}`,
          upsert_nodes: [projectionNodeWire(absorbed)],
          remove_node_ids: [],
          upsert_edges: [
            {
              id: "absorbed-into",
              kind: "absorbed_into",
              from_node_id: "absorbed-s",
              to_node_id: "successor",
              canonical: true,
              hidden_count: 0,
              expandable: false,
            },
          ],
          remove_edge_ids: [],
          invalidate_slice_keys: [],
        },
      });
    });

    await waitFor(() => {
      expect(result.current.canvas?.graph.nodes.map((node) => node.id)).toEqual([
        "successor",
      ]);
      expect(result.current.canvas?.graph.nodes[0]?.merged_from).toEqual([
        "absorbed-s",
      ]);
    });
  });
});
