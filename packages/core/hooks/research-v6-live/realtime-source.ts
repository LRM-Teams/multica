import type { WSConnectionStatus } from "../../api/ws-client";
import { parseResearchV6Delta } from "../../research-v6/schemas";
import type { ResearchV6Delta } from "../../types/research-v6";
import type { LiveConnectionStatus, ResearchV6LiveSource } from "./types";

/**
 * Real-time subscription shape exposed by the WSProvider realtime bus.
 * Kept as a structural interface so this module does not import React.
 */
export interface RealtimeBus {
  /** Subscribe to a WS event; returns an unsubscribe function. */
  subscribeEvent: (event: string, handler: (payload: unknown) => void) => () => void;
  /** Register a callback run on a successful WS (re)connection. */
  onBusReconnect: (cb: () => void) => () => void;
  /** Observe the authenticated socket lifecycle, not merely subscriptions. */
  onBusConnectionStatus: (cb: (status: WSConnectionStatus) => void) => () => void;
}

/** The WS event that carries V6 graph projection updates. */
export const RESEARCH_V6_GRAPH_UPDATED_EVENT = "research_session:graph_updated" as const;

export interface RealtimeLiveSourceOptions {
  /** Treat explicit unsubscribe as a clean disconnect (vs a dropped socket). */
  markStatus?: (status: LiveConnectionStatus) => void;
}

/**
 * Default production live source backed by the WSProvider realtime bus.
 *
 * Every `research_session:graph_updated` frame is parsed as a
 * `ResearchV6Delta` and pushed through; unparseable frames are dropped
 * gracefully (lenient wire, matching the schemas convention) so one bad frame
 * never tears down the projection. When the bus reconnects, the caller
 * re-runs the server resume with the last confirmed sequence.
 */
export function createRealtimeLiveSource(
  bus: RealtimeBus,
  options: RealtimeLiveSourceOptions = {},
): ResearchV6LiveSource {
  const onDeltaListeners = new Set<(delta: ResearchV6Delta) => void>();
  const reconnectListeners = new Set<() => void>();
  const statusListeners = new Set<(s: LiveConnectionStatus) => void>();

  const emitStatus = (status: LiveConnectionStatus) => {
    for (const listener of statusListeners) listener(status);
    options.markStatus?.(status);
  };

  return {
    connect(onDelta) {
      onDeltaListeners.add(onDelta);
      const unsubEvent = bus.subscribeEvent(RESEARCH_V6_GRAPH_UPDATED_EVENT, (payload) => {
        const delta = parseResearchV6Delta(payload);
        if (!delta) return;
        for (const listener of onDeltaListeners) listener(delta);
      });
      const unsubStatus = bus.onBusConnectionStatus((status) => {
        if (status === "connected") emitStatus("connected");
        else if (status === "connecting") emitStatus("connecting");
        else if (status === "disconnected") emitStatus("disconnected");
        else emitStatus("idle");
      });

      return {
        disconnect: () => {
          unsubEvent();
          unsubStatus();
          onDeltaListeners.delete(onDelta);
          emitStatus("disconnected");
        },
      };
    },

    onReconnect(handler) {
      reconnectListeners.add(handler);
      const unsub = bus.onBusReconnect(() => {
        emitStatus("reconnecting");
        for (const listener of reconnectListeners) listener();
      });
      return () => {
        reconnectListeners.delete(handler);
        unsub();
      };
    },

    onStatusChange(handler) {
      statusListeners.add(handler);
      return () => {
        statusListeners.delete(handler);
      };
    },
  };
}
