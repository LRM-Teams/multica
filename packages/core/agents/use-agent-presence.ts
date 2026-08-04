"use client";

import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { agentListOptions } from "../workspace/queries";
import { runtimeListOptions } from "../runtimes/queries";
import { agentDetailOptions, agentTaskSnapshotOptions } from "./queries";
import {
  buildPresenceMap,
  deriveAgentPresenceDetail,
  runtimeReachabilityFromAgent,
} from "./derive-presence";
import type { AgentPresenceDetail } from "./types";

// 30s tick, mirroring useRuntimeHealth. Presence depends on wall-clock time
// for one reason: `unstable` (= RuntimeHealth.recently_lost) decays into
// `offline` at the 5-minute mark with no new server data. Without a tick the
// transition would only render on the next unrelated query update.
// The earlier 2-minute "clear failed badge" tick was removed when failed
// became sticky; this one re-introduces ticking with a different motivation.
const PRESENCE_TICK_MS = 30_000;

function usePresenceTick(): number {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), PRESENCE_TICK_MS);
    return () => clearInterval(id);
  }, []);
  return tick;
}

/**
 * Workspace-wide presence map keyed by `agent.id`. **The single entry point
 * for any list / card / runtime sub-view that needs presence for more than
 * one agent.**
 *
 * Why this exists (vs calling `useAgentPresence` per row): the per-agent
 * hook subscribes to 3 queries. With 30+ rows that's a forest of redundant
 * memos. This batch hook pays the cost once for the whole page; rows just
 * `Map.get(id)` — O(1) reads, no extra subscriptions.
 *
 * Returned value:
 *   - `byAgent`: ready-to-read Map. Empty if data is still loading.
 *   - `loading`: true until all three input queries have resolved at least
 *      once. Callers can render skeletons during loading.
 *
 * Single-agent consumers should keep using `useAgentPresenceDetail`; this
 * hook is for surfaces that already have a list of agents in hand.
 */
export function useWorkspacePresenceMap(wsId: string | undefined): {
  byAgent: Map<string, AgentPresenceDetail>;
  loading: boolean;
} {
  const { data: agents, isPending: agentsPending, isError: agentsErr } = useQuery({
    ...agentListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const { data: runtimes, isPending: runtimesPending, isError: runtimesErr } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const { data: snapshot, isPending: snapshotPending, isError: snapshotErr } = useQuery({
    ...agentTaskSnapshotOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const tick = usePresenceTick();

  const byAgent = useMemo(() => {
    // Treat errored queries as empty so the map still builds — a 404 on
    // the snapshot endpoint shouldn't leave every row's presence blank.
    const safeAgents = agents ?? (agentsErr ? [] : null);
    const safeRuntimes = runtimes ?? (runtimesErr ? [] : null);
    const safeSnapshot = snapshot ?? (snapshotErr ? [] : null);
    if (!safeAgents || !safeRuntimes || !safeSnapshot) {
      return new Map<string, AgentPresenceDetail>();
    }
    return buildPresenceMap({
      agents: safeAgents,
      runtimes: safeRuntimes,
      snapshot: safeSnapshot,
      now: Date.now(),
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agents, runtimes, snapshot, agentsErr, runtimesErr, snapshotErr, tick]);

  return {
    byAgent,
    // "loading" only while the queries are genuinely pending — once they
    // settle (success OR error), we render with whatever we have. This
    // matches the detail-version behaviour: don't spin forever on errors.
    loading:
      (agentsPending && !agentsErr) ||
      (runtimesPending && !runtimesErr) ||
      (snapshotPending && !snapshotErr),
  };
}

/**
 * Single-agent presence detail: availability + last task state + counts +
 * (when failed) failure reason and timestamp. Returns "loading" only while
 * the underlying queries haven't resolved yet — a missing runtime is a
 * real state (offline) and resolves into a non-loading detail.
 *
 * For surfaces that already have a list of agents in hand (Agents page,
 * Runtime detail), prefer `useWorkspacePresenceMap` to avoid forest of
 * redundant subscriptions.
 */
// Synthesised fallback shown when we can't resolve a real agent (deleted,
// archived, or referenced by stale data) but still need to render something
// next to the avatar. Yields a gray dot + idle last-task — better than a
// skeleton spinning forever.
const MISSING_AGENT_DETAIL: AgentPresenceDetail = {
  availability: "offline",
  workload: "idle",
  runningCount: 0,
  queuedCount: 0,
  capacity: 0,
};

export function useAgentPresenceDetail(
  wsId: string | undefined,
  agentId: string | undefined,
): AgentPresenceDetail | "loading" {
  const { data: agents, isError: agentsErr } = useQuery({
    ...agentListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const { data: runtimes, isError: runtimesErr } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  const { data: snapshot, isError: snapshotErr } = useQuery({
    ...agentTaskSnapshotOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  // LRM-548 / LRM-292: ListAgents hides channel / group-manager agents
  // (LRM-233). Profile still opens via GetAgent — when the directory misses,
  // derive presence from the detail cache instead of a hard offline stub.
  const listMiss =
    !!wsId &&
    !!agentId &&
    !!agents &&
    !agentsErr &&
    !agents.some((a) => a.id === agentId);
  const { data: detailAgent, isPending: detailPending, isError: detailErr } = useQuery({
    ...agentDetailOptions(wsId ?? "", agentId ?? ""),
    enabled: listMiss,
  });
  const tick = usePresenceTick();

  return useMemo<AgentPresenceDetail | "loading">(() => {
    if (!wsId || !agentId) return "loading";

    // Treat query errors as "no data" rather than "still loading". A 404 /
    // 5xx on the snapshot endpoint (e.g. backend hasn't deployed the new
    // route yet) used to leave the UI spinning forever; now we degrade to
    // an empty list and the dot still renders based on runtime health.
    const safeAgents = agents ?? (agentsErr ? [] : null);
    const safeRuntimes = runtimes ?? (runtimesErr ? [] : null);
    const safeSnapshot = snapshot ?? (snapshotErr ? [] : null);
    if (!safeAgents || !safeRuntimes || !safeSnapshot) return "loading";

    const agentFromList = safeAgents.find((a) => a.id === agentId);
    const agent = agentFromList ?? (listMiss && !detailErr ? detailAgent : undefined);
    if (listMiss && detailPending && !detailAgent) return "loading";
    // Agent referenced but not in the workspace's active list and detail
    // miss/forbidden (archived assignee, deleted, …). Gray-offline fallback.
    if (!agent) return MISSING_AGENT_DETAIL;
    // Missing from the runtime list is NOT the same as offline when the agent
    // carries a presence-safe status projection (LRM-248 AC5).
    const runtime =
      safeRuntimes.find((r) => r.id === agent.runtime_id) ??
      runtimeReachabilityFromAgent(agent);

    const tasks = safeSnapshot.filter((t) => t.agent_id === agentId);
    return deriveAgentPresenceDetail({ agent, runtime, tasks, now: Date.now() });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    wsId,
    agentId,
    agents,
    runtimes,
    snapshot,
    agentsErr,
    runtimesErr,
    snapshotErr,
    listMiss,
    detailAgent,
    detailPending,
    detailErr,
    tick,
  ]);
}
