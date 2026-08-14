import { describe, expect, it, beforeEach } from "vitest";
import {
  ResearchV6ProjectionClient,
} from "./projection-client";
import type {
  ResearchV6Delta,
  ResearchV6ProjectionNode,
  ResearchV6ProjectionEdge,
  ResearchV6Snapshot,
} from "../../types/research-v6";

/** Deterministic fixture builder for tests. */
export function makeNode(seq: number, id: string): ResearchV6ProjectionNode {
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

export function makeEdge(seq: number, id: string, from: string, to: string): ResearchV6ProjectionEdge {
  return {
    id,
    run_id: "run-1",
    from_node_id: from,
    to_node_id: to,
    edge_type: "depends_on",
    created_sequence: seq,
    tombstoned_at_sequence: null,
  };
}

export function makeSnapshot(through: number, nodes: ResearchV6ProjectionNode[], edges: ResearchV6ProjectionEdge[]): ResearchV6Snapshot {
  return {
    snapshot_id: `snap-${through}`,
    run_id: "run-1",
    through_event_sequence: through,
    graph_content_hash: {
      nodes: nodes.map((n) => n.id).join(","),
      edges: edges.map((e) => e.id).join(","),
    },
    nodes,
    edges,
    next_cursor: null,
  };
}

export function makeDelta(
  from: number,
  through: number,
  opts: { nodes?: ResearchV6ProjectionNode[]; edges?: ResearchV6ProjectionEdge[]; nodeTombstones?: string[]; edgeTombstones?: string[]; graphContentHash?: { nodes: string; edges: string } | null } = {},
): ResearchV6Delta {
  return {
    from_sequence_exclusive: from,
    through_sequence: through,
    graph_content_hash: opts.graphContentHash,
    node_upserts: opts.nodes ?? [],
    edge_upserts: opts.edges ?? [],
    node_tombstones: opts.nodeTombstones ?? [],
    edge_tombstones: opts.edgeTombstones ?? [],
    affected_root_node_ids: [],
    transition_kind: null,
  };
}

/** Deterministic fake gap scheduler. */
function createFakeScheduler() {
  const fired: Array<() => void> = [];
  const cancelled: boolean[] = [];
  return {
    schedule: (cb: () => void) => {
      fired.push(cb);
      const idx = fired.length - 1;
      return { cancel: () => { cancelled[idx] = true; } };
    },
    fireAll() {
      fired.forEach((cb, i) => { if (!cancelled[i]) cb(); });
    },
    fireNext() {
      for (let i = 0; i < fired.length; i++) {
        if (!cancelled[i]) { const cb = fired[i]!; cancelled[i] = true; cb(); return; }
      }
    },
  };
}

function freshClient(scheduler: ReturnType<typeof createFakeScheduler> = createFakeScheduler()) {
  return new ResearchV6ProjectionClient({
    gapTimeoutMs: 100,
    scheduleGapTimeout: scheduler.schedule,
  });
}

describe("ResearchV6ProjectionClient", () => {
  let scheduler: ReturnType<typeof createFakeScheduler>;

  beforeEach(() => {
    scheduler = createFakeScheduler();
  });

  describe("server projection hash", () => {
    it("advances to the through-sequence hash after a contiguous delta", () => {
      const client = freshClient();
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));

      client.applyDelta(makeDelta(0, 1, {
        nodes: [makeNode(1, "n1")],
        graphContentHash: { nodes: "nodes-at-1", edges: "edges-at-1" },
      }));

      expect(client.getState().graphContentHash).toEqual({ nodes: "nodes-at-1", edges: "edges-at-1" });
    });

    it("clears a stale snapshot hash when an older server omits the delta hash", () => {
      const client = freshClient();
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));

      client.applyDelta(makeDelta(0, 1, { nodes: [makeNode(1, "n1")] }));

      expect(client.getState().graphContentHash).toBeNull();
    });
  });

  describe("duplicate delta idempotency (AC #1)", () => {
    it("applying the same delta twice does not duplicate nodes or edges", () => {
      const client = freshClient();
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));

      const delta = makeDelta(0, 1, {
        nodes: [makeNode(1, "n1")],
        edges: [makeEdge(1, "e1", "root", "n1")],
      });

      const first = client.applyDelta(delta);
      expect(first.kind).toBe("applied");
      expect(client.hasNode("n1")).toBe(true);
      expect(client.hasEdge("e1")).toBe(true);

      const dup = client.applyDelta(delta);
      expect(dup.kind).toBe("duplicate");

      const { nodes, edges } = client.getState();
      expect([...nodes.values()].filter((n) => n.id === "n1")).toHaveLength(1);
      expect([...edges.values()].filter((e) => e.id === "e1")).toHaveLength(1);
    });

    it("an older delta (fully behind confirmed) is dropped as duplicate", () => {
      const client = freshClient();
      client.applySnapshot(makeSnapshot(2, [makeNode(0, "root"), makeNode(1, "n1"), makeNode(2, "n2")], []));
      const result = client.applyDelta(makeDelta(0, 1, { nodes: [makeNode(1, "n1")] }));
      expect(result.kind).toBe("duplicate");
      expect(client.hasNode("n1")).toBe(true);
      expect(client.hasNode("n2")).toBe(true);
    });
  });

  describe("out-of-order delta buffering + ordered commit (AC #2a)", () => {
    it("buffers a future delta then commits once the missing middle fills", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));

      // Delta for event 3 arrives first (gap ahead: events 1,2 missing).
      const late = makeDelta(2, 3, { nodes: [makeNode(3, "n3")] });
      const buffered = client.applyDelta(late);
      expect(buffered.kind).toBe("buffered");
      expect(client.getState().awaitingSequence).toBe(1);
      expect(client.hasNode("n3")).toBe(false);

      // Missing middle events arrive — fill the gap, commit in order.
      const e1 = client.applyDelta(makeDelta(0, 1, { nodes: [makeNode(1, "n1")] }));
      expect(e1.kind).toBe("applied");
      expect(client.getState().lastConfirmedSequence).toBe(1);
      expect(client.hasNode("n3")).toBe(false); // still gapped on event 2

      const e2 = client.applyDelta(makeDelta(1, 2, { nodes: [makeNode(2, "n2")] }));
      expect(e2.kind).toBe("applied");
      expect(client.getState().lastConfirmedSequence).toBe(3);
      expect(client.hasNode("n1")).toBe(true);
      expect(client.hasNode("n2")).toBe(true);
      expect(client.hasNode("n3")).toBe(true);
      expect(client.getState().pendingDeltas.size).toBe(0);
    });

    it("multiple out-of-order deltas drain fully in sequence order", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));

      client.applyDelta(makeDelta(3, 4, { nodes: [makeNode(4, "n4")] }));
      client.applyDelta(makeDelta(1, 2, { nodes: [makeNode(2, "n2")] }));
      client.applyDelta(makeDelta(0, 1, { nodes: [makeNode(1, "n1")] }));
      // Drain event 2 then buffered 3-4.
      client.applyDelta(makeDelta(2, 3, { nodes: [makeNode(3, "n3")] }));

      const state = client.getState();
      expect(state.lastConfirmedSequence).toBe(4);
      expect(state.pendingDeltas.size).toBe(0);
      expect(client.hasNode("n1")).toBe(true);
      expect(client.hasNode("n2")).toBe(true);
      expect(client.hasNode("n3")).toBe(true);
      expect(client.hasNode("n4")).toBe(true);
    });
  });

  describe("gap detection → resync (AC #2b)", () => {
    it("an unfilled gap timeouts to exactly one observable resync", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));

      client.applyDelta(makeDelta(5, 6, { nodes: [makeNode(6, "n6")] })); // gap (awaiting 1)
      expect(client.getState().resyncRequestedCount).toBe(0);

      scheduler!.fireAll();
      const state = client.getState();
      expect(state.resyncRequestedCount).toBe(1); // one observable resync
      expect(state.awaitingSequence).toBeNull();
    });

    it("a straddle delta (permanent gap) triggers one resync immediately", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(2, [makeNode(0, "root"), makeNode(1, "n1"), makeNode(2, "n2")], []));
      // from <= confirmed (0 <= 2) but through > confirmed (3 > 2) — server
      // compacted history we already consumed.
      const result = client.applyDelta(makeDelta(0, 3, { nodes: [makeNode(3, "n3")] }));
      expect(result.kind).toBe("resync_required");
      expect(client.getState().resyncRequestedCount).toBe(1);
    });

    it("fires only one resync even when the timer fires repeatedly", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));
      client.applyDelta(makeDelta(7, 8, { nodes: [makeNode(8, "n8")] }));
      scheduler!.fireAll();
      scheduler!.fireAll(); // second expiry must stay coalesced
      expect(client.getState().resyncRequestedCount).toBe(1);
    });
  });

  describe("reconnect from last confirmed sequence (AC #3)", () => {
    it("continues contiguously when the server still holds history", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(2, [makeNode(0, "root"), makeNode(1, "n1"), makeNode(2, "n2")], []));

      let offeredSeq = -1;
      const result = client.reconnect((seq) => {
        offeredSeq = seq;
        return { ok: true, resyncRequired: false };
      });

      expect(offeredSeq).toBe(2); // carried last confirmed sequence
      expect(result.kind === "duplicate" || result.kind === "applied").toBe(true);
      expect(client.getState().resyncRequestedCount).toBe(0);
    });

    it("requests resync when the server can no longer resume contiguously", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(2, [makeNode(0, "root"), makeNode(1, "n1"), makeNode(2, "n2")], []));
      const result = client.reconnect(() => ({ ok: false, resyncRequired: true }));
      expect(result.kind).toBe("resync_required");
      expect(client.getState().resyncRequestedCount).toBe(1);
    });

    it("coalesces a resync from reconnect + a duplicate-straddle into one request", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(1, [makeNode(0, "root"), makeNode(1, "n1")], []));
      client.reconnect(() => ({ ok: false, resyncRequired: true }));
      client.applyDelta(makeDelta(0, 2, { nodes: [makeNode(2, "n2")] }));
      expect(client.getState().resyncRequestedCount).toBe(1);
    });
  });

  describe("resync lifecycle", () => {
    it("applySnapshot resets buffered deltas and advances confirmed sequence", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));
      client.applyDelta(makeDelta(3, 4, { nodes: [makeNode(4, "n4")] }));
      expect(client.getState().pendingDeltas.size).toBe(1);

      client.applySnapshot(makeSnapshot(10, [makeNode(0, "root"), makeNode(10, "n10")], []));
      const state = client.getState();
      expect(state.lastConfirmedSequence).toBe(10);
      expect(state.pendingDeltas.size).toBe(0);
      expect(client.hasNode("n4")).toBe(false);
      expect(client.hasNode("n10")).toBe(true);
    });

    it("ackResyncCompleted clears the requested flag for the next cycle", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));
      client.applyDelta(makeDelta(2, 3, { nodes: [makeNode(3, "n3")] }));
      scheduler!.fireAll();
      expect(client.getState().resyncRequestedCount).toBe(1);
      client.ackResyncCompleted();
      expect(client.getState().resyncRequestedCount).toBe(0);
    });
  });

  describe("edge and node tombstone visibility", () => {
    it("tombstoning an edge drops it from visible edges but keeps the id recorded", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));
      const edge = makeEdge(1, "e1", "root", "root");
      client.applyDelta(makeDelta(0, 1, { edges: [edge] }));
      expect(client.hasEdge("e1")).toBe(true);

      const tombstone = { ...edge, tombstoned_at_sequence: 2 };
      client.applyDelta(makeDelta(1, 2, { edges: [tombstone] }));
      expect(client.hasEdge("e1")).toBe(false);
      expect(client.getState().tombstonedEdgeIds.has("e1")).toBe(true);
    });

    it("node tombstone removes the node", () => {
      const client = freshClient(scheduler!);
      client.applySnapshot(makeSnapshot(0, [makeNode(0, "root")], []));
      client.applyDelta(makeDelta(0, 1, { nodes: [makeNode(1, "n1")] }));
      expect(client.hasNode("n1")).toBe(true);
      client.applyDelta(makeDelta(1, 2, { nodeTombstones: ["n1"] }));
      expect(client.hasNode("n1")).toBe(false);
    });
  });

});
