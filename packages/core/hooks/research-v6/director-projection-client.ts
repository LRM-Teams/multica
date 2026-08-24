import type {
  ResearchV6DirectorDensityBin,
  ResearchV6DirectorProjectionDelta,
  ResearchV6DirectorProjectionEdge,
  ResearchV6DirectorProjectionNode,
  ResearchV6DirectorProjectionSnapshot,
} from "../../types/research-v6-director";

export interface ResearchV6DirectorProjectionView {
  sliceKey: string;
  nodes: ReadonlyMap<string, ResearchV6DirectorProjectionNode>;
  edges: ReadonlyMap<string, ResearchV6DirectorProjectionEdge>;
  densityBins: ReadonlyMap<string, ResearchV6DirectorDensityBin>;
  hasMore: boolean;
  nextCursor: string | null;
}

export interface ResearchV6DirectorProjectionClientState {
  workspaceId: string | null;
  runId: string | null;
  snapshotId: string | null;
  projectionHash: string | null;
  lastConfirmedSequence: number;
  views: ReadonlyMap<string, ResearchV6DirectorProjectionView>;
  pendingDeltas: ReadonlyMap<number, ResearchV6DirectorProjectionDelta>;
  awaitingSequence: number | null;
  resyncRequired: boolean;
}

export type ResearchV6DirectorApplyResult =
  | { kind: "applied"; advancedTo: number }
  | { kind: "duplicate" }
  | { kind: "buffered"; awaitingSequence: number }
  | { kind: "resync_required"; reason: string };

export interface ResearchV6DirectorProjectionClientOptions {
  gapTimeoutMs?: number;
  scheduleGapTimeout?: (
    callback: () => void,
    delayMs: number,
  ) => { cancel(): void };
}

const DEFAULT_GAP_TIMEOUT_MS = 8_000;

/**
 * Ordered display cache for the authoritative Director V6 Projection.
 * Canonical state is never created here: this class only applies strict server
 * snapshots/deltas and invalidates server-declared slice keys.
 */
export class ResearchV6DirectorProjectionClient {
  private workspaceId: string | null = null;
  private runId: string | null = null;
  private snapshotId: string | null = null;
  private projectionHash: string | null = null;
  private primarySliceKey: string | null = null;
  private lastConfirmedSequence = 0;
  private views = new Map<string, ResearchV6DirectorProjectionView>();
  private pendingDeltas = new Map<number, ResearchV6DirectorProjectionDelta>();
  private awaitingSequence: number | null = null;
  private resyncRequired = false;
  private committedInvalidatedSliceKeys = new Set<string>();
  private gapTimer: { cancel(): void } | null = null;
  private readonly gapTimeoutMs: number;
  private readonly scheduleGapTimeout: NonNullable<
    ResearchV6DirectorProjectionClientOptions["scheduleGapTimeout"]
  >;

  constructor(options: ResearchV6DirectorProjectionClientOptions = {}) {
    this.gapTimeoutMs = options.gapTimeoutMs ?? DEFAULT_GAP_TIMEOUT_MS;
    this.scheduleGapTimeout =
      options.scheduleGapTimeout ??
      ((callback, delayMs) => {
        const timer = globalThis.setTimeout(callback, delayMs);
        return { cancel: () => globalThis.clearTimeout(timer) };
      });
  }

  /** Replace with a new snapshot, or append another page of the same slice. */
  applySnapshotPage(snapshot: ResearchV6DirectorProjectionSnapshot): void {
    if (
      this.runId !== null &&
      (this.runId !== snapshot.runId || this.workspaceId !== snapshot.workspaceId)
    ) {
      this.requireResync("projection_identity_mismatch");
      return;
    }
    if (
      this.snapshotId === snapshot.snapshotId &&
      (this.projectionHash !== snapshot.projectionHash ||
        this.lastConfirmedSequence !== snapshot.throughEventSequence)
    ) {
      this.requireResync("stale_snapshot_page");
      return;
    }
    const sameProjection =
      this.snapshotId === snapshot.snapshotId &&
      this.workspaceId === snapshot.workspaceId &&
      this.runId === snapshot.runId &&
      this.projectionHash === snapshot.projectionHash &&
      this.lastConfirmedSequence === snapshot.throughEventSequence;

    if (!sameProjection) {
      this.views.clear();
      this.pendingDeltas.clear();
      this.committedInvalidatedSliceKeys.clear();
      this.workspaceId = snapshot.workspaceId;
      this.runId = snapshot.runId;
      this.snapshotId = snapshot.snapshotId;
      this.projectionHash = snapshot.projectionHash;
      this.lastConfirmedSequence = snapshot.throughEventSequence;
      this.primarySliceKey = snapshot.sliceKey;
      this.resyncRequired = false;
      this.cancelGapTimer();
    }

    const current = this.views.get(snapshot.sliceKey);
    const nodes = new Map(current?.nodes ?? []);
    const edges = new Map(current?.edges ?? []);
    const densityBins = new Map(current?.densityBins ?? []);
    for (const node of snapshot.nodes) nodes.set(node.id, node);
    for (const edge of snapshot.edges) edges.set(edge.id, edge);
    for (const bin of snapshot.densityBins) densityBins.set(bin.id, bin);
    this.views.set(snapshot.sliceKey, {
      sliceKey: snapshot.sliceKey,
      nodes,
      edges,
      densityBins,
      hasMore: snapshot.hasMore,
      nextCursor: snapshot.nextCursor ?? null,
    });
  }

  /** Start an explicit full repair, even when the server reuses snapshot identity. */
  replaceWithSnapshotPage(snapshot: ResearchV6DirectorProjectionSnapshot): void {
    this.views.clear();
    this.pendingDeltas.clear();
    this.workspaceId = null;
    this.runId = null;
    this.snapshotId = null;
    this.projectionHash = null;
    this.primarySliceKey = null;
    this.lastConfirmedSequence = 0;
    this.resyncRequired = false;
    this.committedInvalidatedSliceKeys.clear();
    this.cancelGapTimer();
    this.applySnapshotPage(snapshot);
  }

  /** Invalidations committed while applying one frame and draining its buffer. */
  takeCommittedInvalidatedSliceKeys(): string[] {
    const keys = [...this.committedInvalidatedSliceKeys];
    this.committedInvalidatedSliceKeys.clear();
    return keys;
  }

  applyDelta(delta: ResearchV6DirectorProjectionDelta): ResearchV6DirectorApplyResult {
    if (this.resyncRequired) {
      return { kind: "resync_required", reason: "resync_already_required" };
    }
    if (!this.matchesProjection(delta)) {
      return this.requireResync("projection_identity_mismatch");
    }
    if (delta.eventSequence <= this.lastConfirmedSequence) {
      return { kind: "duplicate" };
    }
    const expected = this.lastConfirmedSequence + 1;
    if (delta.eventSequence > expected) {
      const existing = this.pendingDeltas.get(delta.eventSequence);
      if (existing && existing.projectionHash !== delta.projectionHash) {
        return this.requireResync("conflicting_buffered_delta");
      }
      this.pendingDeltas.set(delta.eventSequence, delta);
      this.armGapTimer();
      return { kind: "buffered", awaitingSequence: expected };
    }
    if (delta.previousProjectionHash !== this.projectionHash) {
      return this.requireResync("projection_hash_chain_mismatch");
    }
    if (
      this.primarySliceKey !== null &&
      delta.invalidateSliceKeys.includes(this.primarySliceKey)
    ) {
      return this.requireResync("primary_slice_invalidated");
    }
    this.commit(delta);
    if (!this.drainPending()) {
      return { kind: "resync_required", reason: "projection_hash_chain_mismatch" };
    }
    return { kind: "applied", advancedTo: this.lastConfirmedSequence };
  }

  requireServerResync(): ResearchV6DirectorApplyResult {
    return this.requireResync("server_resync_required");
  }

  getState(): ResearchV6DirectorProjectionClientState {
    return {
      workspaceId: this.workspaceId,
      runId: this.runId,
      snapshotId: this.snapshotId,
      projectionHash: this.projectionHash,
      lastConfirmedSequence: this.lastConfirmedSequence,
      views: this.views,
      pendingDeltas: this.pendingDeltas,
      awaitingSequence: this.awaitingSequence,
      resyncRequired: this.resyncRequired,
    };
  }

  private matchesProjection(delta: ResearchV6DirectorProjectionDelta): boolean {
    return (
      this.snapshotId !== null &&
      delta.workspaceId === this.workspaceId &&
      delta.runId === this.runId &&
      delta.snapshotId === this.snapshotId
    );
  }

  private commit(delta: ResearchV6DirectorProjectionDelta): void {
    for (const sliceKey of delta.invalidateSliceKeys) {
      this.committedInvalidatedSliceKeys.add(sliceKey);
    }
    for (const [sliceKey, view] of this.views) {
      if (delta.invalidateSliceKeys.includes(sliceKey)) {
        this.views.delete(sliceKey);
        continue;
      }
      const nodes = new Map(view.nodes);
      const edges = new Map(view.edges);
      for (const node of delta.upsertNodes) {
        if (sliceKey === this.primarySliceKey || nodes.has(node.id)) {
          nodes.set(node.id, node);
        }
      }
      for (const nodeId of delta.removeNodeIds) nodes.delete(nodeId);
      for (const edge of delta.upsertEdges) {
        if (sliceKey === this.primarySliceKey || edges.has(edge.id)) {
          edges.set(edge.id, edge);
        }
      }
      for (const edgeId of delta.removeEdgeIds) edges.delete(edgeId);
      this.views.set(sliceKey, { ...view, nodes, edges });
    }
    this.projectionHash = delta.projectionHash;
    this.lastConfirmedSequence = delta.eventSequence;
    this.pendingDeltas.delete(delta.eventSequence);
    this.cancelGapTimer();
  }

  private drainPending(): boolean {
    let next = this.pendingDeltas.get(this.lastConfirmedSequence + 1);
    while (next) {
      if (
        next.previousProjectionHash !== this.projectionHash ||
        (this.primarySliceKey !== null &&
          next.invalidateSliceKeys.includes(this.primarySliceKey))
      ) {
        this.requireResync(
          next.previousProjectionHash !== this.projectionHash
            ? "projection_hash_chain_mismatch"
            : "primary_slice_invalidated",
        );
        return false;
      }
      this.commit(next);
      next = this.pendingDeltas.get(this.lastConfirmedSequence + 1);
    }
    if (this.pendingDeltas.size > 0) this.armGapTimer();
    return true;
  }

  private armGapTimer(): void {
    if (this.gapTimer) return;
    this.awaitingSequence = this.lastConfirmedSequence + 1;
    this.gapTimer = this.scheduleGapTimeout(() => {
      this.gapTimer = null;
      this.requireResync("delta_gap_timeout");
    }, this.gapTimeoutMs);
  }

  private requireResync(reason: string): ResearchV6DirectorApplyResult {
    this.resyncRequired = true;
    this.pendingDeltas.clear();
    this.cancelGapTimer();
    return { kind: "resync_required", reason };
  }

  private cancelGapTimer(): void {
    this.gapTimer?.cancel();
    this.gapTimer = null;
    this.awaitingSequence = null;
  }
}
