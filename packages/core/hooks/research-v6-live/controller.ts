import type {
  ResearchV6Delta,
  ResearchV6ProjectionTransport,
  ResearchV6Snapshot,
} from "../../types/research-v6";
import { ResearchV6ProjectionClient } from "../research-v6/projection-client";
import type {
  LiveConnectionStatus,
  LiveSourceDisconnect,
  ResearchV6LiveProjectionControllerOptions,
  ResearchV6LiveSource,
} from "./types";

/**
 * Research V6 · Live projection controller (LRM-1483 / FE-07).
 *
 * Framework-agnostic engine behind `useResearchV6LiveProjection`. It owns the
 * real-time connection lifecycle and keeps CONNECTION state strictly apart
 * from DATA state:
 *
 *  - the live source streams deltas → they flow into the pure projection
 *    client (ordered, idempotent, gap-aware) — that is the data cache;
 *  - the controller tracks `connecting / connected / reconnecting /
 *    disconnected` separately and never conflates "the socket dropped" with
 *    "the graph is stale".
 *
 * Reconnect carries the last confirmed sequence (client knows it) and the
 * server either continues contiguously or demands a resync; an unfilled gap or
 * a server-said-resync triggers exactly one observable snapshot resync.
 *
 * The controller is deliberately not React-coupled so gap/resync,
 * connection-error, explicit-cancel and unmount paths are covered by
 * deterministic unit tests. Unmount == `disconnect()`.
 */
export class ResearchV6LiveProjectionController {
  private readonly client: ResearchV6ProjectionClient;
  private readonly transport: ResearchV6ProjectionTransport;
  private readonly live: ResearchV6LiveSource;
  private readonly runId: string;
  private readonly reconnectDelayMs: number;
  private readonly scheduleReconnect: (cb: () => void, afterMs: number) => { cancel(): void };

  private liveHandle: LiveSourceDisconnect | null = null;
  private statusRemover: (() => void) | null = null;
  private reconnectRemover: (() => void) | null = null;
  private reconnectTimer: { cancel(): void } | null = null;
  private gapTimer: { cancel(): void } | null = null;
  private readonly gapTimeoutMs: number;

  private status: LiveConnectionStatus = "idle";
  private destroyed = false;
  private reconnectAttempts = 0;
  private lastSyncedResyncCount = -1;
  private resyncInFlight = false;

  private readonly statusListeners = new Set<(s: LiveConnectionStatus) => void>();
  private readonly snapshotListeners = new Set<(snapshot: ResearchV6Snapshot) => void>();
  private readonly changeListeners = new Set<() => void>();

  constructor(
    runId: string,
    transport: ResearchV6ProjectionTransport,
    live: ResearchV6LiveSource,
    options: ResearchV6LiveProjectionControllerOptions = {},
    client?: ResearchV6ProjectionClient,
  ) {
    this.runId = runId;
    this.transport = transport;
    this.live = live;
    this.reconnectDelayMs = Math.max(0, options.reconnectDelayMs ?? 500);
    const id = globalThis.setTimeout;
    const clear = globalThis.clearTimeout;
    this.scheduleReconnect =
      options.scheduleReconnect ??
      ((cb, ms) => {
        const handle = id(cb, ms);
        return { cancel: () => clear(handle) };
      });
    this.gapTimeoutMs = Math.max(0, options.gapTimeoutMs ?? 8000);
    this.client = client ?? new ResearchV6ProjectionClient({ gapTimeoutMs: options.gapTimeoutMs });
  }

  /** Current connection status (never data state). */
  getConnectionStatus(): LiveConnectionStatus {
    return this.status;
  }

  /** Number of reconnect attempts since the last stable connection. */
  getReconnectAttempts(): number {
    return this.reconnectAttempts;
  }

  /** The underlying projection client (data state). */
  getClient(): ResearchV6ProjectionClient {
    return this.client;
  }

  onStatusChange(handler: (s: LiveConnectionStatus) => void): () => void {
    this.statusListeners.add(handler);
    handler(this.status);
    return () => {
      this.statusListeners.delete(handler);
    };
  }

  /** Called when a fresh snapshot replaces the cache (seed, resync, refresh). */
  onSnapshot(handler: (snapshot: ResearchV6Snapshot) => void): () => void {
    this.snapshotListeners.add(handler);
    return () => {
      this.snapshotListeners.delete(handler);
    };
  }

  /** Fired after any data-cache mutation (delta applied / snapshot resync). */
  onChange(handler: () => void): () => void {
    this.changeListeners.add(handler);
    return () => {
      this.changeListeners.delete(handler);
    };
  }

  /** Establish the live link (idempotent). No-op after `disconnect()`. */
  connect(): void {
    if (this.destroyed) return;
    if (this.liveHandle) return; // already connected
    this.setStatus("connecting");

    this.statusRemover = this.live.onStatusChange((status) => {
      if (status === "connected") {
        this.reconnectAttempts = 0;
        this.setStatus("connected");
      } else if (status === "reconnecting") {
        this.setStatus("reconnecting");
        this.scheduleServerResume();
      } else if (status === "disconnected") {
        this.setStatus("disconnected");
      }
    });

    this.reconnectRemover = this.live.onReconnect(() => this.scheduleServerResume());

    this.liveHandle = this.live.connect((delta) => {
      this.applyDelta(delta);
    });

    // If the source is already connected when we attach, surface that.
    this.setStatus("connected");
    this.observeResync();
  }

  /**
   * Explicit reconnect: resume from the last confirmed sequence. If the source
   * is currently undelivering we still schedule the server resume so the gap /
   * cleared-history paths are exercised deterministically.
   */
  reconnect(): void {
    if (this.destroyed) return;
    this.scheduleServerResume();
  }

  /** Force a fresh snapshot resync (explicit refresh). */
  refresh(): Promise<void> {
    return this.doResync();
  }

  /**
   * Tear the live link down (component unmount / explicit cancel). Idempotent.
   * Emits a single `disconnected` transition so callers can distinguish an
   * intentional stop from data staleness.
   */
  disconnect(): void {
    if (this.destroyed) return;
    this.doDisconnect();
  }

  private doDisconnect(): void {
    if (this.reconnectTimer) {
      this.reconnectTimer.cancel();
      this.reconnectTimer = null;
    }
    this.clearGapTimer();
    if (this.liveHandle) {
      this.liveHandle.disconnect();
      this.liveHandle = null;
    }
    this.statusRemover?.();
    this.statusRemover = null;
    this.reconnectRemover?.();
    this.reconnectRemover = null;
    if (this.status !== "idle" && this.status !== "disconnected") {
      this.setStatus("disconnected");
    }
  }

  /** Release this controller for good (never re-connectable). */
  destroy(): void {
    this.destroyed = true;
    this.doDisconnect();
    this.statusListeners.clear();
    this.snapshotListeners.clear();
    this.changeListeners.clear();
  }

  /* ------------------------------------------------------------------ */

  /** Apply a streaming delta to the data cache (client handles ordering/idempotency). */
  private applyDelta(delta: ResearchV6Delta): void {
    const result = this.client.applyDelta(delta);
    if (result.kind === "buffered") {
      // A delta arrived out of order → a sequence gap is open. Arm our own gap
      // timer so an unfilled gap triggers a snapshot resync even when the pure
      // client's internal timer is insufficient (the controller must be the
      // one to fetch a fresh snapshot, not just flag it).
      this.armGapTimer();
    } else {
      this.clearGapTimer();
    }
    this.notifyChange();
    this.observeResync();
  }

  /**
   * Observe resync requests raised by the pure client (gap timeout / straddle),
   * coalesced to exactly one in-flight fresh-snapshot load per request.
   */
  private observeResync(): void {
    if (this.destroyed) return;
    const count = this.client.getState().resyncRequestedCount;
    if (count <= 0) return;
    if (count === this.lastSyncedResyncCount) return; // already handling
    if (this.resyncInFlight) return;
    this.lastSyncedResyncCount = count;
    void this.doResync();
  }

  /**
   * Load a fresh snapshot, replace the cache and ack the resync request. Never
   * writes a fixture into a production path — always the transport snapshot.
   */
  private async doResync(): Promise<void> {
    if (this.resyncInFlight) return;
    this.resyncInFlight = true;
    try {
      const snapshot = await this.transport.loadSnapshot(this.runId);
      this.client.applySnapshot(snapshot);
      this.client.ackResyncCompleted();
      for (const listener of this.snapshotListeners) listener(snapshot);
      this.notifyChange();
    } catch {
      // Backend not ready — keep current cache; clear the flag so a later
      // request/gap can still be observed instead of getting stuck.
      this.client.ackResyncCompleted();
    } finally {
      this.resyncInFlight = false;
    }
  }

  /**
   * Server resume after a transport (re)connect: offer the last confirmed
   * sequence; either the server continues contiguously or it demands a resync
   * (cleared/compacted history) — handled by the projection client verbatim.
   */
  private scheduleServerResume(): void {
    if (this.destroyed) return;
    if (this.reconnectTimer) return; // one resume at a time
    this.reconnectAttempts += 1;
    this.reconnectTimer = this.scheduleReconnect(() => {
      this.reconnectTimer = null;
      void this.resumeNow();
    }, this.reconnectDelayMs);
  }

  private async resumeNow(): Promise<void> {
    if (this.destroyed) return;
    try {
      const verdict = await this.transport.resume(
        this.runId,
        this.client.getState().lastConfirmedSequence,
      );
      if (verdict.ok) {
        // Contiguous from our position — nothing to replay; deltas flow live.
        this.setStatus("connected");
      } else {
        // Server cleared history / cannot resume contiguously → full resync.
        this.client.requestResync();
        this.observeResync();
      }
    } catch {
      // Resume failed (network error). Stay disconnected; a later reconnect
      // retries. Do not resync on a transport failure — the graph is fine.
      this.setStatus("disconnected");
    }
  }

  private setStatus(status: LiveConnectionStatus): void {
    if (this.status === status) return;
    this.status = status;
    for (const listener of this.statusListeners) listener(status);
    // Connection transitions imply the data view may have advanced (e.g. after
    // a resume), so observers can re-read the client cache.
    this.notifyChange();
  }

  /** Arm a controller-level gap timer; unfilled gap → one snapshot resync. */
  private armGapTimer(): void {
    if (this.gapTimer) return;
    const handle = globalThis.setTimeout(() => {
      this.gapTimer = null;
      if (this.destroyed) return;
      // The pure client flags the gap internally; request a resync through it
      // (coalesced) and let the observer run exactly one fresh snapshot load.
      this.client.requestResync();
      this.observeResync();
    }, this.gapTimeoutMs);
    this.gapTimer = { cancel: () => globalThis.clearTimeout(handle) };
  }

  private clearGapTimer(): void {
    if (this.gapTimer) {
      this.gapTimer.cancel();
      this.gapTimer = null;
    }
  }

  private notifyChange(): void {
    for (const listener of this.changeListeners) listener();
  }
}
