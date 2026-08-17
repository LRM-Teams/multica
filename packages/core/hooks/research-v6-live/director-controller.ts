import type { WSConnectionStatus } from "../../api/ws-client";
import { parseResearchV6DirectorProjectionDelta } from "../../research-v6/director-schemas";
import type {
  ResearchV6DirectorProjectionDelta,
  ResearchV6DirectorProjectionSnapshot,
  ResearchV6DirectorProjectionTransport,
} from "../../types/research-v6-director";
import { ResearchV6DirectorProjectionClient } from "../research-v6/director-projection-client";

export const RESEARCH_V6_DIRECTOR_DELTA_EVENT =
  "research_projection_v6:delta" as const;

export interface ResearchV6DirectorRealtimeBus {
  subscribeEvent(event: string, handler: (payload: unknown) => void): () => void;
  onBusReconnect(handler: () => void): () => void;
  onBusConnectionStatus(
    handler: (status: WSConnectionStatus) => void,
  ): () => void;
}

export type ResearchV6DirectorConnectionStatus =
  | "idle"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "disconnected";

export interface ResearchV6DirectorLiveState {
  connection: ResearchV6DirectorConnectionStatus;
  syncing: boolean;
  malformedFrameCount: number;
}

/**
 * Authenticated realtime consumer for the authoritative Director Projection.
 * It never writes canonical entities to Zustand; all facts pass through the
 * ordered hash-chain client before listeners can render them.
 */
export class ResearchV6DirectorLiveController {
  private readonly client: ResearchV6DirectorProjectionClient;
  private connection: ResearchV6DirectorConnectionStatus = "idle";
  private syncing = false;
  private malformedFrameCount = 0;
  private revision = 0;
  private destroyed = false;
  private unsubscribeEvent: (() => void) | null = null;
  private unsubscribeReconnect: (() => void) | null = null;
  private unsubscribeStatus: (() => void) | null = null;
  private readonly listeners = new Set<() => void>();

  constructor(
    private readonly identity: { workspaceId: string; runId: string },
    private readonly transport: ResearchV6DirectorProjectionTransport,
    private readonly bus: ResearchV6DirectorRealtimeBus,
    private readonly options: {
      onInvalidateSliceKeys?: (sliceKeys: readonly string[]) => void;
      maxResumePages?: number;
    } = {},
    client?: ResearchV6DirectorProjectionClient,
  ) {
    this.client = client ?? new ResearchV6DirectorProjectionClient();
  }

  seedSnapshotPage(snapshot: ResearchV6DirectorProjectionSnapshot): void {
    if (
      snapshot.workspace_id !== this.identity.workspaceId ||
      snapshot.run_id !== this.identity.runId
    ) {
      this.client.requireServerResync();
      this.emit();
      return;
    }
    this.client.applySnapshotPage(snapshot);
    this.emit();
  }

  connect(): void {
    if (this.destroyed || this.unsubscribeEvent) return;
    this.setConnection("connecting");
    this.unsubscribeEvent = this.bus.subscribeEvent(
      RESEARCH_V6_DIRECTOR_DELTA_EVENT,
      (payload) => this.receive(payload),
    );
    this.unsubscribeReconnect = this.bus.onBusReconnect(() => {
      this.setConnection("reconnecting");
      void this.resume();
    });
    this.unsubscribeStatus = this.bus.onBusConnectionStatus((status) => {
      if (status === "connected") this.setConnection("connected");
      else if (status === "connecting") this.setConnection("connecting");
      else if (status === "disconnected") this.setConnection("disconnected");
    });
  }

  disconnect(): void {
    this.unsubscribeEvent?.();
    this.unsubscribeReconnect?.();
    this.unsubscribeStatus?.();
    this.unsubscribeEvent = null;
    this.unsubscribeReconnect = null;
    this.unsubscribeStatus = null;
    this.setConnection("disconnected");
  }

  destroy(): void {
    if (this.destroyed) return;
    this.disconnect();
    this.destroyed = true;
    this.listeners.clear();
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  getClient(): ResearchV6DirectorProjectionClient {
    return this.client;
  }

  getLiveState(): ResearchV6DirectorLiveState {
    return {
      connection: this.connection,
      syncing: this.syncing,
      malformedFrameCount: this.malformedFrameCount,
    };
  }

  getRevision(): number {
    return this.revision;
  }

  resumeNow(): Promise<void> {
    return this.resume();
  }

  private receive(payload: unknown): void {
    const envelope = payload as { run_id?: unknown; delta?: unknown };
    if (
      !envelope ||
      typeof envelope !== "object" ||
      envelope.run_id !== this.identity.runId
    ) {
      return;
    }
    let delta: ResearchV6DirectorProjectionDelta;
    try {
      delta = parseResearchV6DirectorProjectionDelta(envelope.delta);
    } catch {
      this.malformedFrameCount += 1;
      this.client.requireServerResync();
      this.emit();
      void this.resyncSnapshot();
      return;
    }
    this.applyDelta(delta);
  }

  private applyDelta(delta: ResearchV6DirectorProjectionDelta): void {
    const result = this.client.applyDelta(delta);
    if (result.kind === "resync_required") {
      void this.resyncSnapshot();
      this.emit();
      return;
    }
    if (result.kind === "applied") {
      this.options.onInvalidateSliceKeys?.(delta.invalidate_slice_keys);
    }
    this.emit();
  }

  private async resume(): Promise<void> {
    if (this.syncing || this.destroyed) return;
    const state = this.client.getState();
    if (!state.snapshotId || !state.projectionHash) {
      await this.resyncSnapshot();
      return;
    }
    this.syncing = true;
    this.emit();
    try {
      const after = state.lastConfirmedSequence;
      let page = await this.transport.resume(
        this.identity.workspaceId,
        this.identity.runId,
        {
          snapshot_id: state.snapshotId,
          last_confirmed_sequence: after,
          projection_hash: state.projectionHash,
        },
      );
      if (page.resync_required) {
        await this.loadFreshSnapshot();
        return;
      }
      let pages = 0;
      while (true) {
        for (const delta of page.deltas) {
          this.applyDelta(delta);
          if (this.client.getState().resyncRequired) {
            await this.loadFreshSnapshot();
            return;
          }
        }
        if (!page.next_cursor) break;
        pages += 1;
        if (pages >= (this.options.maxResumePages ?? 64)) {
          await this.loadFreshSnapshot();
          return;
        }
        page = await this.transport.loadDeltaPage(
          this.identity.workspaceId,
          this.identity.runId,
          after,
          page.next_cursor,
        );
        if (page.resync_required) {
          await this.loadFreshSnapshot();
          return;
        }
      }
    } catch {
      this.client.requireServerResync();
    } finally {
      this.syncing = false;
      this.emit();
    }
  }

  private async resyncSnapshot(): Promise<void> {
    if (this.syncing || this.destroyed) return;
    this.syncing = true;
    this.emit();
    try {
      await this.loadFreshSnapshot();
    } catch {
      this.client.requireServerResync();
    } finally {
      this.syncing = false;
      this.emit();
    }
  }

  private async loadFreshSnapshot(): Promise<void> {
    const snapshot = await this.transport.loadSnapshot(
      this.identity.workspaceId,
      this.identity.runId,
    );
    this.client.applySnapshotPage(snapshot);
  }

  private setConnection(connection: ResearchV6DirectorConnectionStatus): void {
    if (this.connection === connection) return;
    this.connection = connection;
    this.emit();
  }

  private emit(): void {
    this.revision += 1;
    for (const listener of this.listeners) listener();
  }
}
