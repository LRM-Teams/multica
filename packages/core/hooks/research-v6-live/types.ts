import type { ResearchV6Delta } from "../../types/research-v6";

/**
 * Research V6 · Live Projection connection types (LRM-1483 / FE-07).
 *
 * FE-01 established the projection client (ordered delta application, gap
 * detection, resync, reconnect-with-last-sequence) and a React Query wrapper
 * (`useResearchV6Projection`) whose caller must feed deltas manually. THIS
 * module adds the real-time layer on top: a live source that pushes deltas as
 * they arrive, connection-state tracking kept strictly apart from the data
 * cache, and automatic reconnect carrying the last confirmed sequence.
 *
 * The live source is injectable (mirroring how `ResearchV6ProjectionTransport`
 * is injectable) so the gap/resync, connection-error, explicit-cancel and
 * unmount paths are covered by deterministic tests without needing a live WS
 * stack. The default production source wires to the WSProvider realtime bus
 * (`research_session:graph_updated`) and the Projection resume endpoint.
 */

/** Connection lifecycle is independent from data state. */
export type LiveConnectionStatus =
  | "idle" // never started / torn down
  | "connecting" // attempting to establish / resume
  | "connected" // live source is delivering deltas
  | "reconnecting" // a drop was detected, resume in flight
  | "disconnected"; // explicitly stopped or errored past auto-reconnect

export interface LiveSourceDisconnect {
  /** Stop receiving deltas and cancel reconnect wiring. Idempotent. */
  disconnect(): void;
}

/**
 * Real-time source the live projection consumes. Injectable so tests can drive
 * connection error / cancel / unmount deterministically; the production
 * default is backed by the WSProvider real-time bus.
 */
export interface ResearchV6LiveSource {
  /**
   * Connect and start streaming deltas. `onDelta` receives each delta in wire
   * order; the underlying projection client re-orders / de-duplicates. Returns
   * a handle whose `disconnect()` tears the live link down.
   */
  connect(onDelta: (delta: ResearchV6Delta) => void): LiveSourceDisconnect;
  /** Registers a callback for when the underlying transport reconnects. */
  onReconnect(handler: () => void): () => void;
  /** Emits the current connection status as it changes. */
  onStatusChange(handler: (status: LiveConnectionStatus) => void): () => void;
}

/** Live projection options that do not belong to the React hook surface. */
export interface ResearchV6LiveProjectionControllerOptions {
  /** Milliseconds before an unfilled sequence gap triggers a snapshot resync. */
  gapTimeoutMs?: number;
  /** When false, the live source is not connected automatically. */
  autoConnect?: boolean;
  /** Scheduler override for the reconnect backoff (deterministic tests). */
  scheduleReconnect?: (cb: () => void, afterMs: number) => { cancel(): void };
  /** Reconnect backoff before the first retry (ms). */
  reconnectDelayMs?: number;
}
