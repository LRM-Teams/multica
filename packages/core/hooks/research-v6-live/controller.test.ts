/**
 * @vitest-environment node
 */
import { describe, expect, it, vi, beforeEach } from "vitest";
import { ResearchV6LiveProjectionController } from "./controller";
import type {
  ResearchV6Delta,
  ResearchV6ProjectionNode,
  ResearchV6ProjectionTransport,
  ResearchV6ResumeVerdict,
  ResearchV6Snapshot,
} from "../../types/research-v6";
import type {
  LiveConnectionStatus,
  ResearchV6LiveSource,
} from "./types";

/* ------------------------------------------------------------------ *
 * Deterministic fixtures
 * ------------------------------------------------------------------ */

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

/** Controllable fake live source for deterministic tests. */
function makeLiveSource(config: {
  onConnected?: () => void;
  emitConnectedOnConnect?: boolean;
} = {}): {
  source: ResearchV6LiveSource;
  pushDelta: (d: ResearchV6Delta) => void;
  drop: () => void;
  reconnectBus: () => void;
  connected: () => boolean;
} {
  let onDelta: ((d: ResearchV6Delta) => void) | null = null;
  let reconnectHandler: (() => void) | null = null;
  let statusHandler: ((s: LiveConnectionStatus) => void) | null = null;
  let active = false;

  const emitStatus = (s: LiveConnectionStatus) => {
    statusHandler?.(s);
    if (s === "connected") config.onConnected?.();
  };

  return {
    source: {
      connect(onDeltaCb) {
        onDelta = onDeltaCb;
        active = true;
        if (config.emitConnectedOnConnect !== false) emitStatus("connected");
        return {
          disconnect: () => {
            active = false;
            onDelta = null;
            emitStatus("disconnected");
          },
        };
      },
      onReconnect(handler) {
        reconnectHandler = handler;
        return () => {
          reconnectHandler = null;
        };
      },
      onStatusChange(handler) {
        statusHandler = handler;
        return () => {
          statusHandler = null;
        };
      },
    },
    pushDelta: (d) => {
      if (active) onDelta?.(d);
    },
    drop: () => {
      active = false;
      emitStatus("reconnecting");
      reconnectHandler?.();
    },
    reconnectBus: () => {
      active = true;
      emitStatus("connected");
      reconnectHandler?.();
    },
    connected: () => active,
  };
}

/** Transport with recorded calls and a controllable resume verdict. */
function makeTransport(overrides: {
  snapshots?: ResearchV6Snapshot[];
  resumeVerdict?: ResearchV6ResumeVerdict;
} = {}): {
  transport: ResearchV6ProjectionTransport;
  resumeCalls: Array<{ runId: string; seq: number }>;
  snapshotCalls: number;
  setResumeVerdict: (v: ResearchV6ResumeVerdict) => void;
} {
  const resumeCalls: Array<{ runId: string; seq: number }> = [];
  let snapshotCalls = 0;
  let verdict = overrides.resumeVerdict ?? { ok: true, delta: makeDelta(1, 2, ["n-d2"]) };
  const ordered = (overrides.snapshots ?? []).slice();
  const transport: ResearchV6ProjectionTransport = {
    loadSnapshot: async () => {
      snapshotCalls += 1;
      return ordered.shift() ?? makeSnapshot(2, ["n-snap"]);
    },
    loadDeltaPage: async () => null,
    resume: async (runId, lastConfirmedSequence) => {
      resumeCalls.push({ runId, seq: lastConfirmedSequence });
      return verdict;
    },
  };
  return {
    transport,
    get resumeCalls() {
      return resumeCalls;
    },
    get snapshotCalls() {
      return snapshotCalls;
    },
    setResumeVerdict: (v) => {
      verdict = v;
    },
  };
}

/** Deterministic reconnect scheduler: queue callbacks, flush manually. */
function makeScheduler() {
  const queue: Array<() => void> = [];
  return {
    scheduleReconnect: (cb: () => void) => {
      queue.push(cb);
      return { cancel: () => {} };
    },
    flush: async () => {
      while (queue.length) {
        const cb = queue.shift()!;
        await cb();
      }
    },
  };
}

describe("ResearchV6LiveProjectionController", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  it("auto-connects and streams deltas into the projection cache in order", () => {
    const live = makeLiveSource();
    const { transport } = makeTransport();
    const c = new ResearchV6LiveProjectionController(
      "run-1",
      transport,
      live.source,
      { autoConnect: true, scheduleReconnect: () => ({ cancel: () => {} }) },
    );
    c.connect();
    expect(c.getConnectionStatus()).toBe("connected");

    const base = makeSnapshot(0, ["a"]);
    c.getClient().applySnapshot(base);

    const d1 = makeDelta(0, 1, ["b"]);
    live.pushDelta(d1);
    expect(c.getClient().getState().nodes.has("b")).toBe(true);
    expect(c.getClient().getState().lastConfirmedSequence).toBe(1);
  });

  it("stays connecting until the live source reports an authenticated connection", () => {
    const live = makeLiveSource({ emitConnectedOnConnect: false });
    const { transport } = makeTransport();
    const c = new ResearchV6LiveProjectionController(
      "run-1",
      transport,
      live.source,
      { autoConnect: false },
    );

    c.connect();
    expect(c.getConnectionStatus()).toBe("connecting");
  });

  it("buffers an out-of-order delta and gap-timeout triggers exactly one snapshot resync", async () => {
    const live = makeLiveSource();
    const snapshot = makeSnapshot(0, ["a"]);
    const fresh = makeSnapshot(10, ["a", "z"]);
    // The seed is applied to the client directly (React Query path), so the
    // transport only ever serves the fresh resync snapshot.
    const t = makeTransport({ snapshots: [fresh] });
    const c = new ResearchV6LiveProjectionController("run-1", t.transport, live.source, {
      autoConnect: true,
      gapTimeoutMs: 100,
      scheduleReconnect: () => ({ cancel: () => {} }),
    });
    c.connect();
    c.getClient().applySnapshot(snapshot); // through 0

    // Out-of-order delta: expects sequence 2 but we're at 0 → buffered, gap open.
    const d2 = makeDelta(1, 2, ["c"]);
    live.pushDelta(d2);
    expect(c.getClient().getState().pendingDeltas.size).toBe(1);
    expect(c.getClient().getState().awaitingSequence).toBe(1);

    // Gap timeout → resync requested → fresh snapshot applied (single).
    vi.advanceTimersByTime(200);
    await vi.waitFor(() => {
      expect(c.getClient().getState().lastConfirmedSequence).toBe(10);
    });
    expect(c.getClient().getState().snapshotId).toBe("snap-10");
    // Resync coalesced to one fresh snapshot load.
    expect(t.snapshotCalls).toBe(1);
  });

  it("reconnect carries the last confirmed sequence and resyncs when server demands it", async () => {
    const live = makeLiveSource();
    const base = makeSnapshot(5, ["a"]);
    const fresh = makeSnapshot(9, ["a", "b"]);
    const t = makeTransport({ snapshots: [fresh] });
    const scheduler = makeScheduler();

    const c = new ResearchV6LiveProjectionController("run-1", t.transport, live.source, {
      autoConnect: true,
      scheduleReconnect: scheduler.scheduleReconnect,
      reconnectDelayMs: 0,
    });
    c.getClient().applySnapshot(base); // last confirmed = 5
    c.connect();
    expect(c.getReconnectAttempts()).toBe(0);

    // Server cannot resume contiguously → demands resync.
    t.setResumeVerdict({ ok: false, resync_required: true });
    live.drop();
    await scheduler.flush();

    expect(t.resumeCalls.length).toBe(1);
    expect(t.resumeCalls[0]?.seq).toBe(5); // last confirmed carried
    // Gap/drop triggered exactly one fresh snapshot resync.
    await vi.waitFor(() => {
      expect(c.getClient().getState().lastConfirmedSequence).toBe(9);
    });
    expect(c.getClient().getState().snapshotId).toBe("snap-9");
  });

  it("keeps connection state separate from data state; a drop does not clear data", async () => {
    const live = makeLiveSource();
    const base = makeSnapshot(3, ["a"]);
    const { transport } = makeTransport({ resumeVerdict: { ok: true, delta: makeDelta(4, 4, ["d"]) } });
    const c = new ResearchV6LiveProjectionController("run-1", transport, live.source, {
      autoConnect: true,
      scheduleReconnect: () => ({ cancel: () => {} }),
    });
    c.getClient().applySnapshot(base);
    c.connect();

    const statuses: LiveConnectionStatus[] = [];
    c.onStatusChange((s) => statuses.push(s));

    live.drop(); // reconnecting
    expect(c.getConnectionStatus()).toBe("reconnecting");
    // Data is untouched by the connection drop.
    expect(c.getClient().getState().nodes.has("a")).toBe(true);
    expect(c.getClient().getState().lastConfirmedSequence).toBe(3);
  });

  it("explicit disconnect stops delta delivery and resignals disconnected", () => {
    const live = makeLiveSource();
    const { transport } = makeTransport();
    const c = new ResearchV6LiveProjectionController("run-1", transport, live.source, {
      autoConnect: true,
      scheduleReconnect: () => ({ cancel: () => {} }),
    });
    const base = makeSnapshot(0, []);
    c.getClient().applySnapshot(base);
    c.connect();

    c.disconnect();
    expect(c.getConnectionStatus()).toBe("disconnected");
    // Deltas after disconnect are ignored.
    live.pushDelta(makeDelta(0, 1, ["x"]));
    expect(c.getClient().getState().nodes.has("x")).toBe(false);
  });

  it("destroy (unmount) tears down the live link and stops resync work", async () => {
    const live = makeLiveSource();
    const { transport } = makeTransport();
    const c = new ResearchV6LiveProjectionController("run-1", transport, live.source, {
      autoConnect: true,
      gapTimeoutMs: 100,
      scheduleReconnect: () => ({ cancel: () => {} }),
    });
    const base = makeSnapshot(0, []);
    c.getClient().applySnapshot(base);
    c.connect();

    c.destroy();
    expect(c.getConnectionStatus()).toBe("disconnected");
    // Late deltas / resync requests are ignored after destroy.
    live.pushDelta(makeDelta(0, 1, ["x"]));
    c.getClient().requestResync();
    vi.advanceTimersByTime(200);
    expect(c.getClient().getState().nodes.has("x")).toBe(false);
  });
});
