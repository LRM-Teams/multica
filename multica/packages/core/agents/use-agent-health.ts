"use client";

import { useQuery } from "@tanstack/react-query";
import type { AgentHealthEvent, AgentHealthSummary } from "../types";
import { useCurrentWorkspace } from "../paths";
import { agentHealthOptions } from "./queries";

// Runtime connectivity health hook (#178 / #266 / #271), built against Barry's
// v0.1 contract. A thin wrapper over the single react-query options so consumers
// get a stable shape and never touch query keys directly.
//
// One query fetches summary + events TOGETHER from `GET /api/agents/{id}/health`
// — the presence dot COLOR and the Activity Health block consume the SAME
// source (A1 same-source), never two caches that could drift.
//
// IMPORTANT (transitional): the health API may land AFTER this UI (#271). When
// the endpoint isn't live, the query settles into an error (404) with `data`
// undefined — this hook deliberately does NOT synthesise fake data. Callers
// must degrade gracefully: the dot falls back to its prior color source and the
// Health block shows loading/empty, never a crash. Once the BE is live,
// `summary.state` becomes the SOLE color source for the presence dot.

export interface AgentHealthResult {
  summary: AgentHealthSummary | undefined;
  events: AgentHealthEvent[] | undefined;
  isLoading: boolean;
  isError: boolean;
}

export function useAgentHealth(agentId: string | undefined): AgentHealthResult {
  const wsId = useCurrentWorkspace()?.id;
  const enabled = !!wsId && !!agentId;
  const { data, isPending, isError } = useQuery({
    ...agentHealthOptions(wsId ?? "", agentId ?? ""),
    enabled,
  });
  return {
    summary: data?.health_summary,
    events: data?.health_events,
    // A disabled query (no agent / workspace) reports `isPending` forever;
    // treat "not enabled" as not-loading so the caller renders its fallback
    // instead of an eternal skeleton.
    isLoading: enabled && isPending && !isError,
    isError,
  };
}
