"use client";

import { useQuery } from "@tanstack/react-query";
import { memberPresenceOptions } from "../workspace/queries";
import { agentPresenceOptions } from "./agent-presence";

// Warm the two live Presence snapshots once at the workspace shell. Agent
// Presence no longer prefetches Agent, Runtime, or Task data.
//
// useRealtimeSync patches normal events and performs one reconciliation on
// reconnect; this hook only collapses the cold-start window.
//
// All queries are workspace-scoped; the queryKeys include wsId so workspace
// switch automatically refetches the new workspace's data with no extra
// wiring here. The workspace-scoped layouts on both apps gate rendering on
// "workspace resolved", so callers can safely pass useWorkspaceId() — by the
// time this hook mounts, wsId is guaranteed non-empty.
export function useWorkspacePresencePrefetch(wsId: string | undefined): void {
  useQuery({ ...agentPresenceOptions(wsId ?? ""), enabled: !!wsId });
  // LRM-462: warm human member online set for avatar dots.
  useQuery({ ...memberPresenceOptions(wsId ?? ""), enabled: !!wsId });
}
