import { describe, expect, it, vi } from "vitest";
import type { QueryClient } from "@tanstack/react-query";
import { applyResearchWSEvent } from "./ws-updaters";
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
    getData: () => store.get(JSON.stringify(researchKeys.snapshot("ws", "s1"))),
  } as unknown as QueryClient & { getData: () => unknown };
}

describe("applyResearchWSEvent", () => {
  it("upserts graph nodes without dropping prior nodes", () => {
    const qc = makeQc({
      ...EMPTY_RESEARCH_SNAPSHOT,
      session: { ...EMPTY_RESEARCH_SNAPSHOT.session, id: "s1" },
      nodes: [{ id: "n1", session_id: "s1", node_type: "goal", title: "g", summary: "", status: "active", actor_agent_id: null, payload: {}, created_at: "", updated_at: "" }],
      edges: [],
    });
    applyResearchWSEvent(qc, "ws", {
      type: "research_session:graph_updated",
      payload: {
        session_id: "s1",
        node: { id: "n2", session_id: "s1", node_type: "probe", title: "p", summary: "", status: "active", actor_agent_id: null, payload: {}, created_at: "", updated_at: "" },
      },
    });
    const data = qc.getData() as typeof EMPTY_RESEARCH_SNAPSHOT;
    expect(data.nodes.map((n) => n.id).sort()).toEqual(["n1", "n2"]);
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
      type: "research_session:presence",
      payload: { session_id: "s1", agent_id: "a1", activity: "reading RFC" },
    });
    const presence = store.get(JSON.stringify(researchKeys.presence("ws", "s1"))) as {
      a1: { activity: string };
    };
    expect(presence.a1.activity).toBe("reading RFC");
  });
});
