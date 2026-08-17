// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { useResearchV6DirectorDisplayStore } from "@multica/core/research-v6/director-display-store";
import type { ResearchV6DirectorRealtimeBus } from "@multica/core/research-v6-live/director-controller";
import type {
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
): ResearchV6DirectorProjectionSnapshot {
  return {
    contract_kind: "projection_snapshot",
    schema_version: 6,
    snapshot_id: SNAPSHOT_ID,
    workspace_id: WORKSPACE_ID,
    run_id: RUN_ID,
    through_event_sequence: 4,
    projection_hash: `sha256:${"d".repeat(64)}`,
    slice_key: sliceKey,
    nodes: nodes.map(({ id, tier, expandable }) => ({
      id,
      kind: tier === "S" ? "result_s" : "insight",
      tier,
      canonical_ref: { kind: tier === "S" ? "result" : "insight", id: RUN_ID },
      branch_ids: [],
      state: {
        execution: "succeeded",
        conclusion: "accepted",
        integration: tier === "S" ? "absorbed" : "candidate",
      },
      catalog_summary: id,
      absorbed: tier === "S",
      terminal: true,
      expandable,
      hidden_child_count: expandable ? 1 : 0,
      updated_at: "2026-08-17T08:00:00Z",
    })),
    edges: [],
    density_bins: [],
    has_more: false,
  };
}

function wrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

describe("useResearchV6DirectorCanvas", () => {
  beforeEach(() => useResearchV6DirectorDisplayStore.getState().clear());

  it("hydrates the shared graph and reveals one server slice on click", async () => {
    const transport = {
      loadSnapshot: async () =>
        snapshot("default", [{ id: "root", tier: "L", expandable: true }]),
      loadSlice: async () =>
        snapshot("expand:root", [{ id: "child", tier: "S", expandable: false }]),
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
    await waitFor(() => expect(result.current.canvas?.graph.nodes).toHaveLength(1));
    act(() => result.current.expansionControl?.onToggleNode("root"));
    await waitFor(() => expect(result.current.canvas?.graph.nodes).toHaveLength(2));
    expect(result.current.expansionControl?.expandedNodeIds.has("root")).toBe(
      true,
    );
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
      { id: "live", tier: "S", expandable: false },
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
          previous_projection_hash: initial.projection_hash,
          projection_hash: `sha256:${"e".repeat(64)}`,
          upsert_nodes: [nextNode],
          remove_node_ids: [],
          upsert_edges: [],
          remove_edge_ids: [],
          invalidate_slice_keys: [],
        },
      });
    });
    await waitFor(() => expect(result.current.canvas?.graph.nodes).toHaveLength(2));
  });
});
