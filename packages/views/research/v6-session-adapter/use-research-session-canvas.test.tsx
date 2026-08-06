// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { v5FixtureGraph, v6FixtureSnapshot } from "@multica/core/adapters/fixtures";
import type { ResearchV6Snapshot } from "@multica/core/types/research-v6";
import {
  useResearchSessionCanvas,
  type SessionCanvasTransports,
} from "./use-research-session-canvas";
import type { V5SessionGraphInput } from "./session-adapter";

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
}

function wrapper(client: QueryClient) {
  return function Wrapper({ children }: { children: React.ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

const V5 = v5FixtureGraph();
const V5_INPUT: V5SessionGraphInput = {
  sessionId: "sess-a",
  nodes: V5.nodes,
  edges: V5.edges,
};

const v6Loader = (snapshot: ResearchV6Snapshot) =>
  (_runId: string, _signal?: AbortSignal) => Promise.resolve(snapshot);

const v6Loader404 = () =>
  Promise.reject(Object.assign(new Error("route absent"), { status: 404 }));

const v6LoaderSchemaError = () =>
  Promise.reject(new Error("node.nodes[0].id: expected string, got number"));

describe("useResearchSessionCanvas — LRM-1484 behavior", () => {
  it("V6 probe 404 → falls back to the V5 adapter (only permitted degrade)", async () => {
    const qc = makeClient();
    const transports: SessionCanvasTransports = {
      loadV6Snapshot: v6Loader404,
      loadV5Session: () => Promise.resolve(V5_INPUT),
    };
    const { result } = renderHook(
      () =>
        useResearchSessionCanvas({
          wsId: "w",
          sessionId: "sess-a",
          runId: "run-a",
          transports,
        }),
      { wrapper: wrapper(qc) },
    );
    await waitFor(() => expect(result.current.status).toBe("v5"));
    expect(result.current.verdict?.kind).toBe("fallback-v5");
    // The snapshot is the unified V5 canvas.
    expect(result.current.canvas?.source).toBe("v5");
    expect(result.current.snapshot.snapshotId).toBe("v5:sess-a:0");
    expect(result.current.error).toBeNull();
  });

  it("V6 probe success → v6 source with the unified V6 snapshot", async () => {
    const qc = makeClient();
    const snapshot = v6FixtureSnapshot();
    const transports: SessionCanvasTransports = {
      loadV6Snapshot: v6Loader(snapshot),
      loadV5Session: () => Promise.resolve(V5_INPUT),
    };
    const { result } = renderHook(
      () =>
        useResearchSessionCanvas({
          wsId: "w",
          sessionId: "sess-a",
          runId: "run-a",
          transports,
        }),
      { wrapper: wrapper(qc) },
    );
    await waitFor(() => expect(result.current.status).toBe("v6"));
    expect(result.current.canvas?.source).toBe("v6");
    expect(result.current.snapshot.snapshotId).toBe("v6-snap-1");
    expect(result.current.snapshot.throughEventSequence).toBe(6);
  });

  it("V6 200 but schema error → interface error, never silent V5 fallback", async () => {
    const qc = makeClient();
    const transports: SessionCanvasTransports = {
      loadV6Snapshot: v6LoaderSchemaError,
      // Even a valid V5 source must NOT be used to hide the interface error.
      loadV5Session: () => Promise.resolve(V5_INPUT),
    };
    const { result } = renderHook(
      () =>
        useResearchSessionCanvas({
          wsId: "w",
          sessionId: "sess-a",
          runId: "run-a",
          transports,
        }),
      { wrapper: wrapper(qc) },
    );
    await waitFor(() => expect(result.current.status).toBe("error"));
    expect(result.current.error?.kind).toBe("interface-error");
    // No source is claimed.
    expect(result.current.source).toBeNull();
    // The empty snapshot, not fabricated V5 data.
    expect(result.current.snapshot.nodes).toHaveLength(0);
  });

  it("switching session cancels old work: new session never reuses the old snapshot", async () => {
    const qc = makeClient();
    // V6 route is absent for BOTH sessions → V5 fallback, so we can observe the
    // V5 snapshot is sourced from the new session, not carried over.
    const transports: SessionCanvasTransports = {
      loadV6Snapshot: v6Loader404,
      loadV5Session: (sessionId) =>
        Promise.resolve({
          sessionId,
          nodes: V5.nodes.map((n) =>
            n.id === "goal" ? { ...n, title: `goal-${sessionId}` } : n,
          ),
          edges: V5.edges,
        }),
    };
    const { result, rerender } = renderHook(
      ({ sessionId }: { sessionId: string }) =>
        useResearchSessionCanvas({
          wsId: "w",
          sessionId,
          runId: "run-a",
          transports,
        }),
      {
        wrapper: wrapper(qc),
        initialProps: { sessionId: "sess-a" },
      },
    );
    await waitFor(() => expect(result.current.status).toBe("v5"));

    // Switch to a new session — stale response for sess-a must not overwrite it.
    rerender({ sessionId: "sess-b" });
    await waitFor(() =>
      expect(result.current.snapshot.snapshotId).toBe("v5:sess-b:0"),
    );
    const goal = result.current.snapshot.nodes.find((n) => n.id === "goal");
    expect(goal?.title).toBe("goal-sess-b");
    expect(result.current.snapshot.snapshotId).not.toBe("v5:sess-a:0");
  });

  it("without a runId or V6 loader it uses V5 directly (no probe crash)", async () => {
    const qc = makeClient();
    const transports: SessionCanvasTransports = {
      loadV5Session: () => Promise.resolve(V5_INPUT),
    };
    const { result } = renderHook(
      () =>
        useResearchSessionCanvas({
          wsId: "w",
          sessionId: "sess-a",
          transports,
        }),
      { wrapper: wrapper(qc) },
    );
    await waitFor(() => expect(result.current.status).toBe("v5"));
    expect(result.current.verdict?.kind).toBe("fallback-v5");
  });

  it("reportingUnknownVersionNotUsed: probe abort (switching) does not surface as error", async () => {
    const qc = makeClient();
    // A V5 loader so there is always a valid fallback once the probe settles.
    const transports: SessionCanvasTransports = {
      loadV6Snapshot: () =>
        new Promise<ResearchV6Snapshot>((resolve) => {
          setTimeout(() => resolve(v6FixtureSnapshot()), 50);
        }),
      loadV5Session: () => Promise.resolve(V5_INPUT),
    };
    const { result, unmount } = renderHook(
      () =>
        useResearchSessionCanvas({
          wsId: "w",
          sessionId: "sess-a",
          runId: "run-a",
          transports,
        }),
      { wrapper: wrapper(qc) },
    );
    await act(async () => {
      unmount();
    });
    // Unmounting cancels the in-flight V6 probe; no unhandled interface error.
    expect(result.current.error).toBeNull();
  });
});
