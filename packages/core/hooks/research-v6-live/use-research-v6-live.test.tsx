/**
 * @vitest-environment jsdom
 */
import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { useResearchV6LiveProjection } from "./use-research-v6-live";
import type {
  ResearchV6Delta,
  ResearchV6ProjectionNode,
  ResearchV6ProjectionTransport,
  ResearchV6Snapshot,
} from "../../types/research-v6";
import type { ResearchV6LiveSource } from "./types";
import { useResearchV6ProjectionStore } from "../research-v6/store";

vi.mock("../../realtime", () => ({
  useWS: () => ({
    subscribe: vi.fn(() => () => {}),
    onReconnect: vi.fn(() => () => {}),
  }),
}));

function makeNode(seq: number, id: string): ResearchV6ProjectionNode {
  return {
    id,
    run_id: "run-1",
    entity_kind: "task",
    entity_id: id,
    node_kind: "task",
    node_subtype: "",
    schema_version: 1,
    title: `node ${id}`,
    summary: `summary ${id}`,
    status: "running",
    importance: 1,
    freshness: null,
    contract_version: "1",
    plan_version: "1",
    strategy_version: "1",
    actor_agent_id: null,
    task_id: null,
    attempt_id: null,
    created_at: null,
    updated_at: null,
    cost: null,
    detail: { id },
    created_sequence: seq,
    updated_sequence: seq,
    terminal_sequence: null,
  };
}

function makeSnapshot(through: number, nodeIds: string[]): ResearchV6Snapshot {
  return {
    snapshot_id: `snap-${through}`,
    run_id: "run-1",
    through_event_sequence: through,
    graph_content_hash: { nodes: "n", edges: "e" },
    nodes: nodeIds.map((id) => makeNode(through, id)),
    edges: [],
    next_cursor: null,
  };
}

function makeDelta(from: number, through: number, nodeIds: string[]): ResearchV6Delta {
  return {
    from_sequence_exclusive: from,
    through_sequence: through,
    node_upserts: nodeIds.map((id) => makeNode(through, id)),
    edge_upserts: [],
    node_tombstones: [],
    edge_tombstones: [],
    affected_root_node_ids: [nodeIds[0] ?? "root"],
    transition_kind: null,
  };
}

/** Fake transport + live source with recorded calls. */
function makeHarness(seed: ResearchV6Snapshot) {
  const resumeCalls: Array<{ runId: string; seq: number }> = [];
  let disconnectCount = 0;
  let connectCount = 0;
  let onDelta: ((d: ResearchV6Delta) => void) | null = null;
  let reconnectHandlers: Array<() => void> = [];
  let statusHandlers: Array<(status: import("./types").LiveConnectionStatus) => void> = [];
  let active = false;

  const transport: ResearchV6ProjectionTransport = {
    loadSnapshot: async () => seed,
    loadDeltaPage: async () => null,
    resume: async (runId, lastConfirmedSequence) => {
      resumeCalls.push({ runId, seq: lastConfirmedSequence });
      return { ok: true, delta: makeDelta(1, 1, ["n2"]) };
    },
  };

  const live: ResearchV6LiveSource = {
    connect(onDeltaCb) {
      connectCount += 1;
      onDelta = onDeltaCb;
      active = true;
      for (const handler of statusHandlers.slice()) handler("connected");
      return {
        disconnect: () => {
          disconnectCount += 1;
          active = false;
          onDelta = null;
          for (const handler of statusHandlers.slice()) handler("disconnected");
        },
      };
    },
    onReconnect(handler) {
      reconnectHandlers.push(handler);
      return () => {
        reconnectHandlers = reconnectHandlers.filter((h) => h !== handler);
      };
    },
    onStatusChange(handler) {
      statusHandlers.push(handler);
      return () => {
        statusHandlers = statusHandlers.filter((h) => h !== handler);
      };
    },
  };

  return {
    transport,
    live,
    get resumeCalls() {
      return resumeCalls;
    },
    get disconnectCount() {
      return disconnectCount;
    },
    get connectCount() {
      return connectCount;
    },
    pushDelta: (d: ResearchV6Delta) => {
      if (active) onDelta?.(d);
    },
    fireReconnect: () => {
      for (const h of reconnectHandlers.slice()) h();
    },
  };
}

function wrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

describe("useResearchV6LiveProjection", () => {
  beforeEach(() => {
    useResearchV6ProjectionStore.setState({ runs: {}, clients: {} });
  });

  it("seeds from the React Query snapshot and streams deltas into the projection cache", async () => {
    const queryClient = new QueryClient();
    const h = makeHarness(makeSnapshot(0, ["a"]));

    const { result } = renderHook(
      () =>
        useResearchV6LiveProjection({
          runId: "run-1",
          wsId: "ws-1",
          transport: h.transport,
          live: h.live,
        }),
      { wrapper: wrapper(queryClient) },
    );

    // Snapshot seeded from React Query (server state).
    await waitFor(() => {
      expect(result.current.nodes.has("a")).toBe(true);
    });
    expect(result.current.liveStatus).toBe("connected");
    expect(result.current.lastConfirmedSequence).toBe(0); // snapshot through 0

    // Stream a contiguous delta → applied to the cache, no duplication.
    act(() => {
      h.pushDelta(makeDelta(0, 1, ["b"]));
    });
    expect(result.current.nodes.has("b")).toBe(true);
    expect(result.current.lastConfirmedSequence).toBe(1);

    // Duplicate delta does not duplicate nodes.
    act(() => {
      h.pushDelta(makeDelta(0, 1, ["b"]));
    });
    expect(result.current.nodes.has("b")).toBe(true);
    // Still exactly the two seeded/streamed nodes.
    expect(result.current.nodes.size).toBe(2);
  });

  it("keeps connection state separate from data state", async () => {
    const queryClient = new QueryClient();
    const h = makeHarness(makeSnapshot(3, ["a"]));

    const { result } = renderHook(
      () =>
        useResearchV6LiveProjection({
          runId: "run-1",
          wsId: "ws-1",
          transport: h.transport,
          live: h.live,
        }),
      { wrapper: wrapper(queryClient) },
    );

    // Wait for BOTH the connection and the seeded data to land.
    await waitFor(() => {
      expect(result.current.liveStatus).toBe("connected");
      expect(result.current.nodes.size).toBeGreaterThan(0);
    });
    expect(result.current.disconnected).toBe(false);
    const dataSizeBefore = result.current.nodes.size;

    // A connection transition (reconnect) does not clear or alter data.
    act(() => {
      h.fireReconnect();
    });
    expect(result.current.nodes.size).toBe(dataSizeBefore);
    expect(result.current.nodes.has("a")).toBe(true);
  });

  it("reconnect carries the last confirmed sequence to the server resume", async () => {
    const queryClient = new QueryClient();
    const h = makeHarness(makeSnapshot(3, ["a", "b"]));

    const { result } = renderHook(
      () =>
        useResearchV6LiveProjection({
          runId: "run-1",
          wsId: "ws-1",
          transport: h.transport,
          live: h.live,
        }),
      { wrapper: wrapper(queryClient) },
    );
    await waitFor(() => {
      expect(result.current.liveStatus).toBe("connected");
    });

    act(() => {
      result.current.reconnect();
    });
    await waitFor(() => {
      expect(h.resumeCalls.length).toBe(1);
    });
    expect(h.resumeCalls[0]?.runId).toBe("run-1");
    expect(h.resumeCalls[0]?.seq).toBe(3); // last confirmed sequence carried
  });

  it("unmount tears down the live link and removes the run from the store", async () => {
    const queryClient = new QueryClient();
    const h = makeHarness(makeSnapshot(0, ["a"]));

    const { unmount } = renderHook(
      () =>
        useResearchV6LiveProjection({
          runId: "run-1",
          wsId: "ws-1",
          transport: h.transport,
          live: h.live,
        }),
      { wrapper: wrapper(queryClient) },
    );

    await waitFor(() => {
      expect(h.connectCount).toBe(1);
    });

    act(() => {
      unmount();
    });
    expect(h.disconnectCount).toBe(1);
    // Store run torn down on unmount.
    expect(
      useResearchV6ProjectionStore.getState().runs["run-1"],
    ).toBeUndefined();
  });
});
