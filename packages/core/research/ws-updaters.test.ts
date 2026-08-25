import { describe, expect, it, vi } from "vitest";
import type { QueryClient } from "@tanstack/react-query";
import { applyResearchWSEvent } from "./ws-updaters";
import { researchV6DirectorProjectionKeys } from "../hooks/research-v6/director-queries";
import { researchKeys } from "./queries";
import { EMPTY_RESEARCH_SNAPSHOT } from "./schemas";

function makeQc(initial: unknown) {
  const store = new Map<string, unknown>();
  store.set(JSON.stringify(researchKeys.snapshot("ws", "s1")), initial);
  return {
    setQueryData: (_key: unknown, updater: unknown) => {
      const key = JSON.stringify(_key);
      const prev = store.get(key);
      const next = typeof updater === "function" ? (updater as (p: unknown) => unknown)(prev) : updater;
      store.set(key, next);
      return next;
    },
    invalidateQueries: vi.fn(),
    removeQueries: vi.fn(({ queryKey }: { queryKey: unknown }) => {
      store.delete(JSON.stringify(queryKey));
    }),
    getData: () => store.get(JSON.stringify(researchKeys.snapshot("ws", "s1"))),
  } as unknown as QueryClient & { getData: () => unknown };
}

describe("applyResearchWSEvent", () => {
  it("coalesces V6 projection signals and refreshes the authoritative snapshot", () => {
    vi.useFakeTimers();
    const qc = makeQc(EMPTY_RESEARCH_SNAPSHOT);
    const event = {
      type: "research_projection_v6:delta" as const,
      payload: { run_id: "run-v6", through_sequence: 12 },
    };

    applyResearchWSEvent(qc, "ws", event);
    applyResearchWSEvent(qc, "ws", event);
    vi.advanceTimersByTime(499);
    expect(qc.invalidateQueries).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(1);
    expect(qc.invalidateQueries).toHaveBeenCalledWith({
      queryKey: researchV6DirectorProjectionKeys.snapshot("ws", "run-v6"),
    });
    vi.useRealTimers();
  });

  it("coalesces session-list refreshes per workspace to at most once per second", () => {
    vi.useFakeTimers();
    const qc = makeQc(EMPTY_RESEARCH_SNAPSHOT);
    const event = {
      type: "research_session:status_changed" as const,
      payload: { session_id: "s1" },
    };

    applyResearchWSEvent(qc, "ws", event);
    applyResearchWSEvent(qc, "ws", event);
    applyResearchWSEvent(qc, "ws-other", event);

    expect(qc.invalidateQueries).not.toHaveBeenCalledWith({
      queryKey: researchKeys.sessions("ws"),
    });
    vi.advanceTimersByTime(999);
    expect(qc.invalidateQueries).not.toHaveBeenCalledWith({
      queryKey: researchKeys.sessions("ws"),
    });
    vi.advanceTimersByTime(1);
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(2);
    expect(qc.invalidateQueries).toHaveBeenCalledWith({
      queryKey: researchKeys.sessions("ws"),
    });
    expect(qc.invalidateQueries).toHaveBeenCalledWith({
      queryKey: researchKeys.sessions("ws-other"),
    });

    applyResearchWSEvent(qc, "ws", event);
    vi.advanceTimersByTime(1000);
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(3);
    vi.useRealTimers();
  });

  it("upserts graph nodes without dropping prior nodes", () => {
    const qc = makeQc({
      ...EMPTY_RESEARCH_SNAPSHOT,
      session: { ...EMPTY_RESEARCH_SNAPSHOT.session, id: "s1" },
      nodes: [{ id: "n1", session_id: "s1", node_type: "goal", title: "g", summary: "", status: "active", actor_agent_id: null, payload: {}, created_at: "", updated_at: "" }],
      edges: [],
    });
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:graph_updated" as const,
      payload: {
        session_id: "s1",
        node: { id: "n2", session_id: "s1", node_type: "probe", title: "p", summary: "", status: "active", actor_agent_id: null, payload: {}, created_at: "", updated_at: "" },
      },
    });
    const data = qc.getData() as typeof EMPTY_RESEARCH_SNAPSHOT;
    expect(data.nodes.map((n) => n.id).sort()).toEqual(["n1", "n2"]);
    expect(qc.invalidateQueries).toHaveBeenCalledWith({
      queryKey: researchKeys.graphTypedInfinite("ws", "s1"),
    });
  });

  it("patches typed graph cache incrementally when pages are loaded", () => {
    const store = new Map<string, unknown>();
    store.set(JSON.stringify(researchKeys.snapshot("ws", "s1")), {
      ...EMPTY_RESEARCH_SNAPSHOT,
      session: { ...EMPTY_RESEARCH_SNAPSHOT.session, id: "s1" },
      nodes: [{ id: "n1", session_id: "s1", node_type: "goal", title: "g", summary: "", status: "active", actor_agent_id: null, payload: {}, created_at: "", updated_at: "" }],
      edges: [],
    });
    store.set(
      JSON.stringify(researchKeys.graphTypedInfinite("ws", "s1")),
      {
        pages: [
          {
            session_id: "s1",
            graph_version: 2,
            nodes: [{ id: "n1", title: "Goal", node_type: "goal", level: "xxl" }],
            edges: [],
            clusters: [],
            lineage: {
              derived: {},
              merged: {},
              superseded: {},
              restarted: {},
              invalidated: {},
              supersedes: {},
            },
          },
        ],
        pageParams: [0],
      },
    );
    const qc = {
      setQueryData: (_key: unknown, updater: unknown) => {
        const key = JSON.stringify(_key);
        const prev = store.get(key);
        const next =
          typeof updater === "function"
            ? (updater as (value: unknown) => unknown)(prev)
            : updater;
        store.set(key, next);
        return next;
      },
      invalidateQueries: vi.fn(),
      removeQueries: vi.fn(),
    } as unknown as QueryClient & { getData: () => unknown };

    applyResearchWSEvent(qc, "ws", {
      type: "research_session:graph_updated" as const,
      payload: {
        session_id: "s1",
        graph_version: 3,
        node: {
          id: "n2",
          session_id: "s1",
          node_type: "probe",
          title: "Probe",
          summary: "",
          status: "running",
          actor_agent_id: "a1",
          payload: { details: { objective: "Check sources" } },
          level: "s",
          created_at: "",
          updated_at: "",
        },
      },
    });

    const typed = store.get(JSON.stringify(researchKeys.graphTypedInfinite("ws", "s1"))) as {
      pages: Array<{ nodes: Array<{ id: string }>; graph_version: number }>;
    };
    expect(typed.pages[0]?.nodes.map((node) => node.id).sort()).toEqual(["n1", "n2"]);
    expect(typed.pages[0]?.graph_version).toBe(3);
    expect(qc.invalidateQueries).not.toHaveBeenCalled();
  });

  it("rejects a graph entity whose embedded session conflicts with its envelope", () => {
    const qc = makeQc({
      ...EMPTY_RESEARCH_SNAPSHOT,
      session: { ...EMPTY_RESEARCH_SNAPSHOT.session, id: "s1" },
      nodes: [],
    });
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:graph_updated" as const,
      payload: {
        session_id: "s1",
        node: { id: "foreign", session_id: "s2", title: "Other session" },
      },
    });

    const data = qc.getData() as typeof EMPTY_RESEARCH_SNAPSHOT;
    expect(data.nodes).toEqual([]);
    expect(qc.invalidateQueries).toHaveBeenCalledWith({
      queryKey: researchKeys.snapshot("ws", "s1"),
    });
    expect(qc.invalidateQueries).toHaveBeenCalledWith({
      queryKey: researchKeys.graphTypedInfinite("ws", "s1"),
    });
  });

  it("rejects a message whose embedded session conflicts with its envelope", () => {
    const qc = makeQc({
      ...EMPTY_RESEARCH_SNAPSHOT,
      session: { ...EMPTY_RESEARCH_SNAPSHOT.session, id: "s1" },
      messages: [],
    });
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:message" as const,
      payload: {
        session_id: "s1",
        message: { id: "foreign", session_id: "s2", body: "Wrong session" },
      },
    });

    const data = qc.getData() as typeof EMPTY_RESEARCH_SNAPSHOT;
    expect(data.messages).toEqual([]);
    expect(qc.invalidateQueries).toHaveBeenCalledWith({
      queryKey: researchKeys.snapshot("ws", "s1"),
    });
  });

  it("does not write malformed graph and message payloads into the snapshot", () => {
    const qc = makeQc({
      ...EMPTY_RESEARCH_SNAPSHOT,
      session: { ...EMPTY_RESEARCH_SNAPSHOT.session, id: "s1" },
      nodes: [],
      messages: [],
    });
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:graph_updated" as const,
      payload: { session_id: "s1", node: { id: "missing-kind" } },
    });
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:message" as const,
      payload: { session_id: "s1", message: { body: "missing id" } },
    });

    const data = qc.getData() as typeof EMPTY_RESEARCH_SNAPSHOT;
    expect(data.nodes).toEqual([]);
    expect(data.messages).toEqual([]);
    expect(qc.invalidateQueries).toHaveBeenCalledWith({
      queryKey: researchKeys.snapshot("ws", "s1"),
    });
  });

  it("removes snapshot cache when session is deleted", () => {
    vi.useFakeTimers();
    const qc = makeQc({
      ...EMPTY_RESEARCH_SNAPSHOT,
      session: { ...EMPTY_RESEARCH_SNAPSHOT.session, id: "s1" },
    });
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:status_changed" as const,
      payload: { session_id: "s1", deleted: true },
    });
    expect(qc.getData()).toBeUndefined();
    expect(qc.removeQueries).toHaveBeenCalledWith({
      queryKey: researchKeys.graphTypedInfinite("ws", "s1"),
    });
    expect(qc.removeQueries).toHaveBeenCalledWith({
      queryKey: ["research", "ws", "graph-typed", "s1"],
    });
    expect(qc.removeQueries).toHaveBeenCalledWith({
      queryKey: researchKeys.presence("ws", "s1"),
    });
    expect(qc.removeQueries).toHaveBeenCalledWith({
      queryKey: researchKeys.productRounds("ws", "s1"),
    });
    vi.advanceTimersByTime(1000);
    expect(qc.invalidateQueries).toHaveBeenCalled();
    vi.useRealTimers();
  });

  it("stores ephemeral presence by agent", () => {
    const store = new Map<string, unknown>();
    const qc = {
      setQueryData: (_key: unknown, updater: unknown) => {
        const key = JSON.stringify(_key);
        const prev = store.get(key);
        const next = typeof updater === "function" ? (updater as (p: unknown) => unknown)(prev) : updater;
        store.set(key, next);
        return next;
      },
      invalidateQueries: vi.fn(),
    } as unknown as QueryClient;
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:presence" as const,
      payload: { session_id: "s1", agent_id: "a1", activity: "reading RFC", updated_at: 100 },
    });
    const presence = store.get(JSON.stringify(researchKeys.presence("ws", "s1"))) as {
      a1: { activity: string; updatedAt: number };
    };
    expect(presence.a1.activity).toBe("reading RFC");
    expect(presence.a1.updatedAt).toBe(100);
  });

  it("preserves v2 location fields when a legacy WS patch arrives", () => {
    const store = new Map<string, unknown>();
    store.set(JSON.stringify(researchKeys.presence("ws", "s1")), {
      a1: { activity: "starting", updatedAt: 10, phase: "running", role: "scout", fleetMemberId: "fm1", taskId: "task1", nodeId: "node1", branchId: null, stage: "s2_sources", expiresAt: 999, staleReason: null },
    });
    const qc = {
      setQueryData: (_key: unknown, updater: unknown) => {
        const key = JSON.stringify(_key);
        const next = (updater as (value: unknown) => unknown)(store.get(key));
        store.set(key, next);
        return next;
      },
      invalidateQueries: vi.fn(),
    } as unknown as QueryClient;
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:presence" as const,
      payload: { session_id: "s1", agent_id: "a1", activity: "reading RFC", updated_at: 100 },
    });
    expect(store.get(JSON.stringify(researchKeys.presence("ws", "s1")))).toEqual({
      a1: expect.objectContaining({ activity: "reading RFC", updatedAt: 100, phase: "running", nodeId: "node1", taskId: "task1" }),
    });
  });

  it("updates an existing message body in place (streaming upsert)", () => {
    const qc = makeQc({
      ...EMPTY_RESEARCH_SNAPSHOT,
      session: { ...EMPTY_RESEARCH_SNAPSHOT.session, id: "s1" },
      messages: [
        {
          id: "m1",
          session_id: "s1",
          sender_type: "agent",
          sender_id: "a1",
          target_agent_id: null,
          body: "Hel",
          card_kind: "chat",
          meta: { mirrored_from: "chat" },
          created_at: "2026-08-01T00:00:00Z",
        },
      ],
    });
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:message" as const,
      payload: {
        session_id: "s1",
        message: {
          id: "m1",
          session_id: "s1",
          sender_type: "agent",
          sender_id: "a1",
          target_agent_id: null,
          body: "Hello fleet",
          card_kind: "chat",
          meta: { mirrored_from: "chat", stopped: true },
          created_at: "2026-08-01T00:00:00Z",
        },
      },
    });
    const data = qc.getData() as typeof EMPTY_RESEARCH_SNAPSHOT;
    expect(data.messages).toHaveLength(1);
    expect(data.messages[0]?.body).toBe("Hello fleet");
    expect((data.messages[0]?.meta as { stopped?: boolean }).stopped).toBe(true);
  });

  it("clears presence when activity is empty", () => {
    const store = new Map<string, unknown>();
    store.set(JSON.stringify(researchKeys.presence("ws", "s1")), {
      a1: { activity: "busy", updatedAt: 1 },
    });
    const qc = {
      setQueryData: (_key: unknown, updater: unknown) => {
        const key = JSON.stringify(_key);
        const prev = store.get(key);
        const next = typeof updater === "function" ? (updater as (p: unknown) => unknown)(prev) : updater;
        store.set(key, next);
        return next;
      },
      invalidateQueries: vi.fn(),
    } as unknown as QueryClient;
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:presence" as const,
      payload: { session_id: "s1", agent_id: "a1", activity: "", updated_at: 2 },
    });
    const presence = store.get(JSON.stringify(researchKeys.presence("ws", "s1"))) as Record<
      string,
      unknown
    >;
    expect(presence.a1).toBeUndefined();
  });

  it("upserts product-round cards into the dedicated query cache", () => {
    const store = new Map<string, unknown>();
    const qc = {
      setQueryData: (_key: unknown, updater: unknown) => {
        const key = JSON.stringify(_key);
        const prev = store.get(key);
        const next =
          typeof updater === "function"
            ? (updater as (value: unknown) => unknown)(prev)
            : updater;
        store.set(key, next);
        return next;
      },
      invalidateQueries: vi.fn(),
    } as unknown as QueryClient;

    applyResearchWSEvent(qc, "ws", {
      type: "research_session:product_round" as const,
      payload: {
        session_id: "s1",
        card: { id: "round-1", round_number: 1, decision: "continue" },
      },
    });
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:product_round" as const,
      payload: {
        session_id: "s1",
        card: { id: "round-1", round_number: 1, decision: "stop_enough" },
      },
    });

    expect(store.get(JSON.stringify(researchKeys.productRounds("ws", "s1")))).toEqual({
      rounds: [
        expect.objectContaining({
          id: "round-1",
          session_id: "s1",
          round_number: 1,
          decision: "stop_enough",
          coverage_gaps: [],
        }),
      ],
    });
  });
});
