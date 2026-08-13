import type {
  ResearchV6Delta,
  ResearchV6ProjectionEdge,
  ResearchV6ProjectionNode,
  ResearchV6Snapshot,
} from "../../types/research-v6";

export interface ResearchV6ProjectionState {
  /** Stable node id → node. */
  nodes: ReadonlyMap<string, ResearchV6ProjectionNode>;
  /** Stable edge id → visible edge. */
  edges: ReadonlyMap<string, ResearchV6ProjectionEdge>;
  /** Edge ids hidden by visibility tombstone but still known to the server. */
  tombstonedEdgeIds: ReadonlySet<string>;
  /** Highest contiguous event sequence the client has applied. */
  lastConfirmedSequence: number;
  /** Buffered out-of-order deltas keyed by `from_sequence_exclusive`. */
  pendingDeltas: ReadonlyMap<number, ResearchV6Delta>;
  /** Missing sequence we are waiting on, if a gap is open. */
  awaitingSequence: number | null;
  /** Number of resyncs requested (observable, coalesced to one execution). */
  resyncRequestedCount: number;
  snapshotId: string | null;
  graphContentHash: { nodes: string; edges: string } | null;
}

export interface ResyncHandle {
  cancel: () => void;
}

export interface ResearchV6ProjectionClientOptions {
  /**
   * Milliseconds before an unfilled sequence gap triggers resync.
   * Defaults to 8000ms; tests inject a small value / fake scheduler.
   */
  gapTimeoutMs?: number;
  /**
   * Scheduler used to arm/cancel the gap timer. Inject for deterministic
   * tests; defaults to global setTimeout/clearTimeout.
   */
  scheduleGapTimeout?: (cb: () => void, afterMs: number) => ResyncHandle;
}

/** Outcome of applying a delta / reconnect. */
export type ResearchV6ApplyResult =
  | { kind: "applied"; advancedTo: number }
  | { kind: "duplicate" }
  | { kind: "buffered"; awaitingSequence: number }
  | { kind: "resync_required" };

const DEFAULT_GAP_TIMEOUT_MS = 8000;

/**
 * Pure, deterministic client cache for the Research V6 Graph Projection.
 *
 * Consumes the backend Snapshot + Delta and applies by event sequence with:
 *  - idempotency: applying a delta twice never duplicates nodes/edges;
 *  - ordered commit: out-of-order deltas buffer until the missing middle
 *    fills, then commit in sequence order;
 *  - gap detection: an unfilled gap timeouts (or a straddle/permanent gap) to
 *    exactly one observable resync;
 *  - reconnect: resumes from the last confirmed sequence and either continues
 *    contiguously or resyncs when the server cleared history.
 *
 * This cache is client display state. It never writes back to canonical state
 * and never treats display grouping as a real Insight.
 */
export class ResearchV6ProjectionClient {
  private nodes = new Map<string, ResearchV6ProjectionNode>();
  private edges = new Map<string, ResearchV6ProjectionEdge>();
  private tombstonedEdgeIds = new Set<string>();
  private pendingDeltas = new Map<number, ResearchV6Delta>();
  private lastConfirmedSequence = 0;
  private awaitingSequence: number | null = null;
  private resyncRequestedCount = 0;
  private snapshotId: string | null = null;
  private graphContentHash: { nodes: string; edges: string } | null = null;
  private gapTimerHandle: ResyncHandle | null = null;

  private readonly gapTimeoutMs: number;
  private readonly scheduleGapTimeout: (cb: () => void, afterMs: number) => ResyncHandle;

  constructor(options: ResearchV6ProjectionClientOptions = {}) {
    this.gapTimeoutMs = options.gapTimeoutMs ?? DEFAULT_GAP_TIMEOUT_MS;
    this.scheduleGapTimeout =
      options.scheduleGapTimeout ??
      ((cb, ms) => {
        const id = globalThis.setTimeout(cb, ms);
        return { cancel: () => globalThis.clearTimeout(id) };
      });
  }

  /** Reset to a fresh snapshot; buffered deltas are irrelevant afterwards. */
  applySnapshot(snapshot: ResearchV6Snapshot): void {
    this.nodes = new Map(snapshot.nodes.map((n) => [n.id, n]));
    this.edges = new Map(
      snapshot.edges.filter((e) => e.tombstoned_at_sequence === null).map((e) => [e.id, e]),
    );
    this.tombstonedEdgeIds = new Set(
      snapshot.edges
        .filter((e) => e.tombstoned_at_sequence !== null)
        .map((e) => e.id),
    );
    this.pendingDeltas.clear();
    this.lastConfirmedSequence = snapshot.through_event_sequence;
    this.snapshotId = snapshot.snapshot_id;
    this.graphContentHash = snapshot.graph_content_hash;
    this.cancelGapTimer();
  }

  /**
   * Idempotent, ordered, out-of-order-tolerant delta application.
   */
  applyDelta(delta: ResearchV6Delta): ResearchV6ApplyResult {
    // Fully at/before the confirmed sequence → already applied → drop.
    if (delta.through_sequence <= this.lastConfirmedSequence) {
      return { kind: "duplicate" };
    }

    // Contiguous next delta → apply now, then drain anything buffered.
    if (delta.from_sequence_exclusive === this.lastConfirmedSequence) {
      this.commitDelta(delta);
      this.drainPending();
      return { kind: "applied", advancedTo: this.lastConfirmedSequence };
    }

    // Starts after our confirmed position → gap ahead → buffer out of order.
    if (delta.from_sequence_exclusive > this.lastConfirmedSequence) {
      this.pendingDeltas.set(delta.from_sequence_exclusive, delta);
      this.armGapTimerIfNeeded();
      return { kind: "buffered", awaitingSequence: this.lastConfirmedSequence + 1 };
    }

    // Straddles our confirmed boundary (from <= confirmed < through): the
    // server sent a delta starting inside already-consumed history but running
    // past it — server cleared/compacted history we no longer guarantee.
    // We cannot reconstruct a canonical graph from a partial delta → resync.
    this.requestResync();
    return { kind: "resync_required" };
  }

  /**
   * Reconnect flow: the client offers its last confirmed sequence; if the
   * server no longer holds contiguous history it demands a resync.
   */
  reconnect(
    resume: (lastConfirmedSequence: number) => { ok: boolean; resyncRequired?: boolean },
  ): ResearchV6ApplyResult {
    const verdict = resume(this.lastConfirmedSequence);
    if (!verdict.ok || verdict.resyncRequired) {
      this.requestResync();
      return { kind: "resync_required" };
    }
    return { kind: "duplicate" }; // nothing missing to replay.
  }

  /** Request a full snapshot resync, coalescing concurrent requests to one. */
  requestResync(): void {
    if (this.resyncRequestedCount === 0) {
      this.resyncRequestedCount += 1;
    }
    this.cancelGapTimer();
  }

  /** Caller performed the resync + snapshot load; clear the request counter. */
  ackResyncCompleted(): void {
    this.resyncRequestedCount = 0;
    this.cancelGapTimer();
  }

  getState(): ResearchV6ProjectionState {
    return {
      nodes: this.nodes,
      edges: this.edges,
      tombstonedEdgeIds: this.tombstonedEdgeIds,
      lastConfirmedSequence: this.lastConfirmedSequence,
      pendingDeltas: this.pendingDeltas,
      awaitingSequence: this.awaitingSequence,
      resyncRequestedCount: this.resyncRequestedCount,
      snapshotId: this.snapshotId,
      graphContentHash: this.graphContentHash,
    };
  }

  /** Convenience for tests / selectors. */
  hasNode(id: string): boolean {
    return this.nodes.has(id);
  }

  hasEdge(id: string): boolean {
    return this.edges.has(id);
  }

  private commitDelta(delta: ResearchV6Delta): void {
    for (const node of delta.node_upserts) {
      this.nodes.set(node.id, node);
    }
    for (const edge of delta.edge_upserts) {
      this.edges.set(edge.id, edge);
      if (edge.tombstoned_at_sequence !== null) {
        this.edges.delete(edge.id);
        this.tombstonedEdgeIds.add(edge.id);
      }
    }
    for (const id of delta.node_tombstones) {
      this.nodes.delete(id);
    }
    for (const id of delta.edge_tombstones) {
      this.edges.delete(id);
      this.tombstonedEdgeIds.add(id);
    }
    this.lastConfirmedSequence = delta.through_sequence;
    // Older servers omit the post-delta hash. In that case the prior Snapshot
    // hash is stale and must become unknown instead of being misreported.
    this.graphContentHash = delta.graph_content_hash ?? null;
    this.cancelGapTimer();
  }

  /** Apply buffered deltas as their gaps become contiguous. */
  private drainPending(): void {
    let next = this.pendingDeltas.get(this.lastConfirmedSequence);
    while (next && next.from_sequence_exclusive === this.lastConfirmedSequence) {
      this.commitDelta(next);
      this.pendingDeltas.delete(next.from_sequence_exclusive);
      next = this.pendingDeltas.get(this.lastConfirmedSequence);
    }
  }

  private armGapTimerIfNeeded(): void {
    if (this.gapTimerHandle) return;
    this.awaitingSequence = this.lastConfirmedSequence + 1;
    this.gapTimerHandle = this.scheduleGapTimeout(() => {
      this.awaitingSequence = this.lastConfirmedSequence + 1;
      this.requestResync();
    }, this.gapTimeoutMs);
  }

  private cancelGapTimer(): void {
    if (this.gapTimerHandle) {
      this.gapTimerHandle.cancel();
      this.gapTimerHandle = null;
    }
    this.awaitingSequence = null;
  }
}
